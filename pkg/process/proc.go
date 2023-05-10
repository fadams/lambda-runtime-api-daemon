//
// Licensed to the Apache Software Foundation (ASF) under one
// or more contributor license agreements.  See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership.  The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.
//

// Utilities for looking up inodes and pids from the /proc filesystem.
// This is mainly useful to support Extensions when concurrency > 1.
// The approach taken by the Lambda Runtime Interface Daemon is to have
// Runtime instances and any associated extensions running in the same
// process group to make it easier to manage their lifecycles collectively.
// Although each Extension instance has a unique identifier that we may
// use on the next API call, unfortunately there is no identifier that
// associates Runtime instances with Extension instances so we use the
// process  group ID for this purpose. Unfortunately when a Runtime or
// Extension calls its next API call there is no way to directly obtain
// the process group ID of the instance making the API call, so we use
// the address of the calling process and use that to look up the pgid
// from /proc by using the utilities here.

package process

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	// When an entry matches a remote address in /proc/net/tcp the following
	// are the offsets from the matching address to the inode entry we need.
	inodeStart = 87
	inodeEnd   = 95
)

// Given the remote address of a TCP connection (on the current host) search
// /proc/net/tcp for the TCP socket inode associated with that address.
func FindTCPInodeFromAddress(address string) string {
	// Normalise address into the format used in /proc/net/tcp
	// https://www.kernel.org/doc/html/v5.10/networking/proc_net_tcp.html
	// TODO investigate using netlink with sock_diag instead of /proc/net/tcp
	// https://en.wikipedia.org/wiki/Netlink
	// https://man7.org/linux/man-pages/man7/sock_diag.7.html
	// https://kristrev.github.io/2013/07/26/passive-monitoring-of-sockets-on-linux
	// https://www.linuxjournal.com/article/7356
	// https://github.com/svinota/pyroute2
	// Using Netlink is likely faster than reading and parsing /proc/net/tcp
	// but much more of a faff. The performance difference may be marginal
	// as in most cases Extensions will establish connections at startup
	// and reuse them, so in practice this mapping should be infrequent.
	s := strings.Split(address, ":")
	ipdec := strings.Split(s[0], ".") // Get each dotted decimal value
	// Tests for err omitted in Atoi here as args obtained from RemoteAddr()
	// Convert dotted decimal string values to int and reverse the order
	ip := make([]int, 4)
	ip[0], _ = strconv.Atoi(ipdec[3])
	ip[1], _ = strconv.Atoi(ipdec[2])
	ip[2], _ = strconv.Atoi(ipdec[1])
	ip[3], _ = strconv.Atoi(ipdec[0])
	port, _ := strconv.Atoi(s[1])
	// Create address normalised to the format used in /proc/net/tcp
	normalisedAddr := []byte(fmt.Sprintf(": %02X%02X%02X%02X:%04X ",
		ip[0], ip[1], ip[2], ip[3], port))

	//fmt.Println(string(normalisedAddr))

	// Read /proc/net/tcp and search the for entry matching remote address
	// and then extract the inode associated with that entry.
	inode := ""
	if procnettcp, err := os.ReadFile("/proc/net/tcp"); err == nil {
		//fmt.Printf("%s\n", procnettcp)
		// Get index of the normalised address and use that to find inode
		index := bytes.Index(procnettcp, normalisedAddr)
		start := index + inodeStart
		end := index + inodeEnd

		if index != -1 && end < len(procnettcp) {
			inode = string(procnettcp[start:end])
		}
		//fmt.Printf("inode = %s\n", inode)
	}
	return inode
}

// Get pids from /proc by getting the numerical entries from /proc (by
// checking Atoi succeeds) then applying a filter.
func GetPids(filter func(pid int) bool) []int {
	var pids []int
	if proc, err := os.ReadDir("/proc"); err == nil { // Entries of /proc dir
		for _, entry := range proc {
			if pid, err := strconv.Atoi(entry.Name()); err == nil {
				if filter == nil || filter(pid) {
					pids = append(pids, pid)
				}
			} else {
				break // Stop iterating /proc entries after pids
			}
		}
		//fmt.Println(pids)
	}
	return pids
}

// Given a slice of candidate pids and an inode find the pid that matches
// the inode by searching /proc/<PID>/fd for socket:[<inode>].
// The following approach expands upon ideas inspired by:
// https://stackoverflow.com/questions/14667215/finding-a-process-id-given-a-socket-and-inode-in-python-3/14676571#14676571
func FindPidFromInode(pids []int, inode string) int {
	// Given candidate pids iterate them in reverse as the most recently
	// added process is most likely to match the most recent connection.
	for i := len(pids) - 1; i >= 0; i-- {
		pid := pids[i]
		procfd := fmt.Sprintf("/proc/%d/fd", pid)
		//fmt.Println(procfd)
		if entries, err := os.ReadDir(procfd); err == nil {
			// Get the entries of the /proc/<PID>/fd dir in reverse order
			// as the higher fds are more likely to be sockets.
			for j := len(entries) - 1; j >= 0; j-- {
				// Check if the /proc/<PID>/fd/* entry matches inode
				fdpath := procfd + "/" + entries[j].Name()
				if entry, err := os.Readlink(fdpath); err == nil {
					//fmt.Println(entry)
					if entry == "socket:["+inode+"]" {
						return pid
					}
				}
			}
		}
	}
	return 0
}
