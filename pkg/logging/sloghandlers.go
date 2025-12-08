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

// We create custom slog Handlers here https://pkg.go.dev/log/slog#Handler
// to provide different logging "back ends" for structured logging and
// plain human readable logs.

package logging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	// AWS Error response uses RFC3339 timestamps will millisecond precision
	// but https://pkg.go.dev/time#pkg-constants only has RFC3339 & RFC3339Nano
	RFC3339Milli = "2006-01-02T15:04:05.999Z07"
)

// Initialisation for go.uber.org/zap Logger to provide structured logging
// via a custom slog Handler. We use a custom Handler rather than
// go.uber.org/zap/exp/zapslog because that doesn't "pass through" things
// like caller information.
func newZapLogger(w io.Writer, level zapcore.Level) *zap.Logger {
	encoderCfg := zapcore.EncoderConfig{
		TimeKey:        "@timestamp",
		LevelKey:       "level",
		NameKey:        "logger_name",
		CallerKey:      "caller",
		MessageKey:     "message",
		StacktraceKey:  "stacktrace",
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
	}

	jsonEncoder := zapcore.NewJSONEncoder(encoderCfg)

	core := zapcore.NewCore(
		jsonEncoder,
		zapcore.AddSync(w),
		level,
	)

	return zap.New(core, zap.AddCaller(), zap.AddCallerSkip(4))
}

// Custom slog.Handler implementing a go.uber.org/zap back-end
type SlogZapHandler struct {
	Logger *zap.Logger
	level  slog.Level
	attrs  []slog.Attr
}

// Helper to convert slog.Level to zapcore.Level
func slogToZapLevel(l slog.Level) zapcore.Level {
	switch {
	case l <= slog.LevelDebug:
		return zap.DebugLevel
	case l <= slog.LevelInfo:
		return zap.InfoLevel
	case l <= slog.LevelWarn:
		return zap.WarnLevel
	default:
		return zap.ErrorLevel
	}
}

func NewSlogZapHandler(w io.Writer, l slog.Level) *SlogZapHandler {
	logger := newZapLogger(w, slogToZapLevel(l))
	return &SlogZapHandler{Logger: logger, level: l}
}

// Enabled returns true if the given level should be logged
func (h *SlogZapHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

// Handle satisfies slog.Handler
// https://pkg.go.dev/log/slog#Handler
// https://pkg.go.dev/log/slog#Record
// https://pkg.go.dev/log/slog#Logger
func (h *SlogZapHandler) Handle(ctx context.Context, r slog.Record) error {
	fields := make([]zap.Field, 0, 4)

	// Attrs iterator now requires func(Attr) bool
	r.Attrs(func(a slog.Attr) bool {
		fields = append(fields, zap.Any(a.Key, a.Value.Any()))
		return true // continue iteration
	})

	// Append inherited logger attrs (added by WithAttrs)
	for _, a := range h.attrs {
		fields = append(fields, zap.Any(a.Key, a.Value.Any()))
	}

	ce := h.Logger.Check(slogToZapLevel(r.Level), r.Message)
	if ce != nil {
		ce.Write(fields...)
	}
	return nil
}

// WithAttrs returns a new handler with extra attributes (optional)
func (h *SlogZapHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// h2 starts as a shallow copy of dereferenced h. The inner
	// append([]slog.Attr{}, h.attrs...) clones the existing attrs
	// slice. The outer append adds the new attrs to that clone.
	// Result: h2 has its own attrs slice independent of h.
	// https://pkg.go.dev/builtin#append
	// Finally a reterence to our new h2 instance is returned.
	h2 := *h
	h2.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &h2
}

// WithGroup returns a new handler with group name (optional)
func (h *SlogZapHandler) WithGroup(name string) slog.Handler {
	// Simple pass-through for now
	return h
}

//-----------------------------------------------------------------------------

// Custom slog.Handler implementing a formatted Human Readable back-end
// that is close to the original logrus layout.
type SlogPlainHandler struct {
	w     io.Writer
	level slog.Level
	attrs []slog.Attr
}

func NewSlogPlainHandler(w io.Writer, l slog.Level) *SlogPlainHandler {
	return &SlogPlainHandler{w: w, level: l}
}

// Enabled returns true if the given level should be logged
func (h *SlogPlainHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

// Handle satisfies slog.Handler
// https://pkg.go.dev/log/slog#Handler
// https://pkg.go.dev/log/slog#Record
// https://pkg.go.dev/log/slog#Logger
func (h *SlogPlainHandler) Handle(ctx context.Context, r slog.Record) error {
	b := &bytes.Buffer{}

	// Render timestamp.
	time := r.Time.Format(RFC3339Milli)
	fmt.Fprint(b, time)

	// Render level in brackets
	level := strings.ToUpper(r.Level.String())
	fmt.Fprintf(b, " [%s] ", level)

	// Render inherited logger attrs first (added by WithAttrs)
	for _, a := range h.attrs {
		if a.Key == "app" { // Special case for app field
			fmt.Fprintf(b, "(%s) ", a.Value.String())
		} else {
			fmt.Fprintf(b, "%s=%v ", a.Key, a.Value.Any())
		}
	}

	// Render message
	b.WriteString(r.Message)

	// Render attributes
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(b, " %s=%v", a.Key, a.Value.Any())
		return true
	})

	b.WriteByte('\n')
	_, err := h.w.Write(b.Bytes())
	return err
}

// WithAttrs returns a new handler with extra attributes (optional)
func (h *SlogPlainHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// h2 starts as a shallow copy of dereferenced h. The inner
	// append([]slog.Attr{}, h.attrs...) clones the existing attrs
	// slice. The outer append adds the new attrs to that clone.
	// Result: h2 has its own attrs slice independent of h.
	// https://pkg.go.dev/builtin#append
	// Finally a reterence to our new h2 instance is returned.
	h2 := *h
	h2.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &h2
}

// WithGroup returns a new handler with group name (optional)
func (h *SlogPlainHandler) WithGroup(name string) slog.Handler {
	// Simple pass-through for now
	return h
}
