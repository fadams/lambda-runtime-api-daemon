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

package logging

import (
	"bytes"
	"fmt"
	log "github.com/sirupsen/logrus" // Structured logging
	"strings"
)

// TODO find better place for this declaration
const (
	// AWS Error response uses RFC3339 timestamps will millisecond precision
	// but https://pkg.go.dev/time#pkg-constants only has RFC3339 & RFC3339Nano
	RFC3339Milli = "2006-01-02T15:04:05.999Z07"
)

// SetLogLevel sets the log level for internal logging. Needs to be called very
// early during startup to configure logs emitted during initialization
func SetLogLevel(logLevel string) {
	if level, err := log.ParseLevel(logLevel); err == nil {
		log.SetLevel(level)
		log.SetFormatter(&InternalFormatter{})
	} else {
		log.WithError(err).Fatal("Failed to set log level. Valid log levels are:",
			log.AllLevels)
	}
}

type InternalFormatter struct{}

// format RAPID's log
func (f *InternalFormatter) Format(entry *log.Entry) ([]byte, error) {
	b := &bytes.Buffer{}

	// time with comma separator for fraction of second
	time := entry.Time.Format(RFC3339Milli)

	fmt.Fprint(b, time)

	// level
	level := strings.ToUpper(entry.Level.String())
	fmt.Fprintf(b, " [%s]", level)

	// label
	fmt.Fprint(b, " (rapid)")

	// message
	fmt.Fprintf(b, " %s", entry.Message)

	// from WithField and WithError
	for field, value := range entry.Data {
		fmt.Fprintf(b, " %s=%s", field, value)
	}

	fmt.Fprintf(b, "\n")
	return b.Bytes(), nil
}
