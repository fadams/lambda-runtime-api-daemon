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

// The approach taken for structured logging is to move to the standard library
// log/slog using its API for logging in the main body of the code base.
// In this package we initialise logging by creating custom slog Handlers
// https://pkg.go.dev/log/slog#Handler to provide different logging "back ends"
// for both structured logging (via go.uber.org/zap) and plain human readable
// logs that are as similar as possible to the original logrus logging style.

package logging

import (
	"lambda-runtime-api-daemon/pkg/config/util"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// parseLevel converts a string like "debug", "info", "warn", "error"
// (or a numeric level like "-4") into a slog.Level.
// Returns slog.LevelInfo on error.
func parseLevel(s string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToLower(s))); err != nil {
		return slog.LevelInfo
	}
	return level
}

// SetLogLevel sets the log level for internal logging. Needs to be called very
// early during startup to configure logs emitted during initialization
func InitialiseLogging(logLevel string) {
	// Normalise logLevel string and default it to info if not set.
	if logLevel == "" {
		logLevel = "info"
	}
	logLevel = strings.ToLower(logLevel)

	programLevel := new(slog.LevelVar)     // Info by default
	programLevel.Set(parseLevel(logLevel)) // Parse slog logLevel string

	// Set the default logger logging level.
	slog.SetLogLoggerLevel(programLevel.Level())

	// Infer the application name to add to logger label Attr
	appname := "rapid" // Default, abbreviation for lambda-runtime-api-daemon
	if filepath.Base(os.Args[0]) == "lambda-server" {
		appname = "lambda-server"
	}

	// This "label" will be added as an Attr to the loggers
	label := slog.String("app", appname)

	// If USE_STRUCTURED_LOGGING env var is set enable structured logging by
	// configuring slog with a go.uber.org/zap back end via a custom Handler.
	// util.Getenv returns fallback if env var is not set
	if strings.ToLower(
		util.Getenv("USE_STRUCTURED_LOGGING", "false"),
	) == "false" {
		// Set slog Default logger to Human Readable Handler
		handler := NewSlogPlainHandler(os.Stderr, programLevel.Level())
		slog.SetDefault(slog.New(handler).With(label))
	} else {
		// Set slog Default logger to Structured Logging using go.uber.org/zap
		handler := NewSlogZapHandler(os.Stderr, programLevel.Level())
		slog.SetDefault(slog.New(handler).With(label))
	}
}

// Provides a mechanism to call logger.Sync() on the zap.Logger normally
// via defer logging.Sync() in main(). We first get the Handler from the
// Default slog Logger that we've set in SetLogLevel() that may or may not
// be a SlogZapHandler so we do a type assertion to check.
func Sync() {
	handler := slog.Default().Handler()
	if zapHandler, ok := handler.(*SlogZapHandler); ok {
		_ = zapHandler.Logger.Sync()
	}
}
