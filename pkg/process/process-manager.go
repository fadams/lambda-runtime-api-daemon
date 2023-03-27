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

package process

import (
	log "github.com/sirupsen/logrus" // Structured logging
	"os"
	"os/exec"
	"os/signal"
	"sync"
	// According to https://pkg.go.dev/syscall the syscall package is deprecated
	// and callers should use the corresponding package in the golang.org/x/sys
	// repository instead. See https://golang.org/s/go1.4-syscall for more info.
	syscall "golang.org/x/sys/unix"
	"time"
)

// ProcessManager provides a factory for creating child ManagedProcesses that
// are based on exec.Cmd and when started ManagedProcesses register themselves
// with their ProcessManager. ProcessManager also provides HandleSignals(),
// which blocks waiting for application termination signals and is also
// responsible for handling SIGCHLD and subsequently "waiting" for exited
// child processes to "reap" them and prevent them from becoming zombies.
type ProcessManager struct {
	register   func(pid int, nproc int)
	unregister func(pid int, nproc int)
	processes  map[int]*ManagedProcess
	sigchan    chan os.Signal // Signal notification channel.
	sync.RWMutex
}

func NewProcessManager() *ProcessManager {
	pm := &ProcessManager{
		register:   func(pid int, nproc int) {}, // NOOP default implementation
		unregister: func(pid int, nproc int) {}, // NOOP default implementation
		processes:  make(map[int]*ManagedProcess),

		// Set up a channel for signal notifications. The os/signal package uses
		// non-blocking channel sends when delivering signals. If the receiving
		// end of the channel isn't ready and the channel is either unbuffered
		// or full, the signal will not be captured. To avoid missing signals,
		// the channel should be buffered and of the appropriate size. The
		// buffer here is likely "conservatively large" intended to provide
		// some headroom for the case of lots of child processes being
		// teminated resulting in multiple SIGCHLD, though in practice doing
		// Wait4 in a loop until it returns <= 0 likely caters for this
		// scenario so the largeish buffer is somewhat belt & braces.
		// TODO revisit channel buffer size
		sigchan: make(chan os.Signal, 128),
	}
	// Relay incoming signals to the pm.sigchan channel.
	signal.Notify(pm.sigchan, syscall.SIGINT, syscall.SIGKILL, syscall.SIGQUIT,
		syscall.SIGTERM, syscall.SIGSTOP, syscall.SIGTSTP, syscall.SIGCHLD)
	return pm
}

func (pm *ProcessManager) NewManagedProcess(
	args []string, cwd string, env []string) *ManagedProcess {

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Set child process group ID to the new child's process ID.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	return &ManagedProcess{cmd, pm}
}

func (pm *ProcessManager) SetRegisterHandler(handler func(pid int, nproc int)) {
	pm.register = handler
}

func (pm *ProcessManager) SetUnregisterHandler(handler func(pid int, nproc int)) {
	pm.unregister = handler
}

// Register the given ManagedProcess with the ProcessManager.
func (pm *ProcessManager) Register(mp *ManagedProcess) {
	pm.Lock()
	defer pm.Unlock()
	pid := mp.Process.Pid
	pm.processes[pid] = mp
	pm.register(pid, len(pm.processes))
}

// Delete deletes the ManagedProcess for the specified pid from the
// ProcessManager. Note that Delete() *does not* terminate the proces, it
// simply "unmanages" it and Delete() would typically be called after
// successfully waiting (e.g. by calling Wait4) for the process to terminate.
func (pm *ProcessManager) Delete(pid int) {
	pm.Lock() // Need to Lock() rather than RLock() as we are mutating.
	defer pm.Unlock()
	delete(pm.processes, pid)
	pm.unregister(pid, len(pm.processes))
}

// Values returns a slice the values of the map m. The values will be in an
// indeterminate order. This function uses generics and is a direct copy of
// https://pkg.go.dev/golang.org/x/exp/maps#Values from the golang.org/x/exp
// experimental package included here directly to avoid a dependency on an
// experimental package. TODO use maps.Values when promoted to standard library.
func Values[M ~map[K]V, K comparable, V any](m M) []V {
	r := make([]V, 0, len(m))
	for _, v := range m {
		r = append(r, v)
	}
	return r
}

// Returns a slice of ManagedProcess. This is a concurrent-safe wrapper
// for ManagedProcess Values() as we want to be able to iterate in a way
// where iterations could result in Delete() being called, for example
// TerminateAll() or KillAll() could result in SIGCHLD and the handler for
// that will call Delete() for a given pid. Creating the slice of values
// needs to be concurrent-safe, but the slice itself does not.
func (pm *ProcessManager) ManagedProcesses() []*ManagedProcess {
	pm.RLock()
	defer pm.RUnlock()
	return Values(pm.processes)
}

// Iterate the ManagedProcesses calling Terminate()
func (pm *ProcessManager) TerminateAll() {
	for _, mp := range pm.ManagedProcesses() {
		mp.Terminate()
	}
}

// Iterate the ManagedProcesses calling Kill()
func (pm *ProcessManager) KillAll() {
	for _, mp := range pm.ManagedProcesses() {
		mp.Kill()
	}
}

// Handle termination signals and SIGCHLD. The behaviour here is to forward
// termination signals to any child processes and if the signal is SIGCHLD
// call Wait4 to reap the process to prevent them turning into zombies.
func (pm *ProcessManager) HandleSignals() {
	// When a termination signal (SIGINT, SIGTERM, etc.) occurs we will try
	// to cleanly shut down ManagedProcesses, handling subsequent SIGCHLD
	// signals that result in calls to Wait4 to reap the process. It is,
	// however, possible that cleanly shutting down ManagedProcesses might
	// fail so we want to be able to put a deadline on how long we are
	// prepared to wait for ManagedProcesses to exit. We create a timeout
	// channel that is initially nil, which will never deliver a value in
	// the select that is handling signals. When a termination signal occurs
	// we set timeoutCh to the required timeout so the select on the next
	// iteration of the loop will wait either for a signal or the timeout.
	// The approach is inspired by https://stackoverflow.com/a/68757925
	// Found via the Google search: "go time.After infinite"
	var timeoutCh <-chan time.Time

	// When this flag is set we've sent SIGKILL to all ManagedProcesses and
	// it marks one last timeout before we will hard exit regardless.
	killSent := false
loop:
	for {
		select {
		case s := <-pm.sigchan: // Blocks
			switch s {
			case syscall.SIGCHLD:
				// Handle SIGCHLD and use non-blocking Wait4 call to reap
				// children to prevent them turning into zombies. For a small
				// number of processes using cmd.Run() or cmd.Start()+cmd.Wait()
				// might be an alternative, but those block so each child
				// process would need to be managed by its own goroutine. Here
				// we take a more asynchronous approach where we use SIGCHLD to
				// trigger the wait. We need to use Wait4 as that allows
				// waiting for any process (using -1 for the pid argument) and
				// is non-blocking if the syscall.WNOHANG option is set.
				var status syscall.WaitStatus
				var rusage syscall.Rusage
				for {
					// https://man7.org/linux/man-pages/man2/wait4.2.html
					wpid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, &rusage)
					if wpid == 0 || (wpid == -1 && err == syscall.ECHILD) {
						break // No more children to reap, so we are done.
					}

					if err == nil {
						pm.Delete(wpid) // De-register ManagedProcess

						//fmt.Println("len(pm.processes) ", len(pm.processes))
						//fmt.Println(pm.processes)

						// If no ManagedProcesses remain and termination has
						// been signalled then we can exit immediately.
						if len(pm.processes) == 0 && timeoutCh != nil {
							log.Info("Exiting!")
							break loop
						}
					}
				}
			default: // Handle termination signals
				log.Infof("Received %s signal shutting down", s.String())
				// If no ManagedProcesses remain we can exit immediately.
				if len(pm.processes) == 0 {
					log.Info("Exiting!")
					break loop
				}
				// Send SIGTERM to child processes and a deadline to wait for
				// them to terminate. If the deadline expires (case <-timeoutCh
				// in the select statement) we will try killing them.
				pm.TerminateAll()
				timeoutCh = time.After(10 * time.Second)
			}

		case <-timeoutCh:
			if killSent { // If we time out after sending SIGKILL we hard exit.
				log.Warn("Failed to SIGKILL child processes")
				break loop
			}
			// Send SIGKILL to child processes and a deadline to wait for
			// them to die. If the deadline expires we will hard exit.
			pm.KillAll()
			killSent = true
			timeoutCh = time.After(10 * time.Second)
		}
	}
}

type ManagedProcess struct {
	*exec.Cmd
	pm *ProcessManager
}

// Start starts the specified command but does not wait for it to complete.
// If cmd.Start returns successfully, the cmd.Process field will be set.
// After a successful call to Start the ManagedProcess will register itself
// with the parent ProcessManager, keyed by cmd.Process.Pid. ManagedProcesses
// must be Waited for to release associated system resources (prevent them
// from becoming zombies), but this is done by the ProcessManager as part of
// its signal handling by handling SIGCHLD and doing a non-blocking wait4
// to wait for any child processes that may have exited.
func (mp *ManagedProcess) Start() error {
	// Delegate to Cmd.Start and register with ProcessManager if successful.
	if err := mp.Cmd.Start(); err != nil {
		return err
	}
	mp.pm.Register(mp)
	return nil
}

// Send SIGTERM to the ManagedProcess
func (mp *ManagedProcess) Terminate() {
	// Try to send the signal to the process group ID to ensure all related
	// processes are sent the signal, if that errors just send to the process.
	pgid, err := syscall.Getpgid(mp.Cmd.Process.Pid)
	if err == nil {
		// A negative pid sends the signal to all in the process group.
		syscall.Kill(-pgid, syscall.SIGTERM)
	} else {
		mp.Cmd.Process.Signal(syscall.SIGTERM)
	}
}

// Send SIGKILL to the ManagedProcess
func (mp *ManagedProcess) Kill() {
	// Try to send the signal to the process group ID to ensure all related
	// processes are sent the signal, if that errors just send to the process.
	pgid, err := syscall.Getpgid(mp.Cmd.Process.Pid)
	if err == nil {
		// A negative pid sends the signal to all in the process group.
		syscall.Kill(-pgid, syscall.SIGKILL)
	} else {
		mp.Cmd.Process.Signal(syscall.SIGKILL)
	}
}
