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

package runtimeapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"sync"
	// According to https://pkg.go.dev/syscall the syscall package is deprecated
	// and callers should use the corresponding package in the golang.org/x/sys
	// repository instead. See https://golang.org/s/go1.4-syscall for more info.
	syscall "golang.org/x/sys/unix"
	"time"

	// https://pkg.go.dev/github.com/docker/distribution/uuid
	"github.com/docker/distribution/uuid"

	"lambda-runtime-api-daemon/pkg/config/rapid"
	"lambda-runtime-api-daemon/pkg/process"
)

const (
	extensionDir          = "/opt/extensions"
	extensionsDisableFile = "/opt/disable-extensions-jwigqn8j"

	// You can register up to 10 extensions for a function.
	// This limit is enforced through the Register API call.
	// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-extensions-api.html#runtimes-extensions-api-reg
	maxExtensions = 10
)

type Extension struct {
	// awaitRegister is a chan used to await Extension "readiness", e.g. when the
	// "next" Extensions API call is made for a given Extension after it has
	// registered then it is ready to receive events.
	awaitRegister chan struct{}
	events        chan Request // chan of events sent by Runtime API
	timestamp     time.Time
	name          string // Extension file name
	id            string // Extension Identifier as per Register API docs
	// An Extension may be registered for invoke, shutdown, or even for no
	// events. The latter case is most likely used by an Internal Extension,
	// as registering means it will receive SIGTERM but Internal Extensions
	// cannot register for shutdown events. The registered flag allows us
	// to check if an Extension has already registered.
	registered           bool
	invokeIsRegistered   bool
	shutdownIsRegistered bool
}

// Define this type partly as a convenience, but primarily as it allows us to
// define methods to act on all entries of the slice like waiting on channels.
type ExtensionSlice []*Extension

// This method blocks until all Extensions have registered (by closing their
// awaitRegister channel). This allows us to synchronise on Extension
// registration, as the Runtime init phase starts *after* all the Extensions
// have registered as per the Extension API documentation:
// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-extensions-api.html#runtimes-extensions-api-reg
func (es ExtensionSlice) AwaitExtensionRegistration() {
	for _, extension := range es {
		<-extension.awaitRegister // Wait on awaitRegister chan.
	}
}

// Iterates the ExtensionSlice checking if invokeIsRegistered on the Extension
// "handle" and if so sends the supplied invocation to the events chan.
func (es ExtensionSlice) SendInvokeEvent(invocation Request) {
	for _, extension := range es {
		if extension.invokeIsRegistered {
			extension.events <- invocation
		}
	}
}

// Iterates the ExtensionSlice checking if shutdownIsRegistered on the Extension
// "handle" and if so sends a shutdown event to the events chan.
func (es ExtensionSlice) SendShutdownEvent(reason string) {
	shutdown := Request{ID: "SHUTDOWN", Body: []byte(reason)}
	for _, extension := range es {
		if extension.shutdownIsRegistered {
			extension.events <- shutdown
		}
	}
}

// Find the Extension "handle" in the ExtensionSlice that matches name.
// Returns on the first match. A linear search should be fine here as the
// main use of this method is during Extension registration and in any case
// there are generally few Extensions with a maximum of ten allowed.
func (es ExtensionSlice) FindByName(name string) *Extension {
	for _, extension := range es {
		if extension.name == name {
			return extension
		}
	}
	return nil
}

// Iterates the ExtensionSlice checking if any Extension has called its next
// event API method (and therefore has a non-zero timestamp). If so return true,
// as that indicates a transition to the Invoke Phase, if not return false.
func (es ExtensionSlice) RegistrationIsClosed() bool {
	for _, extension := range es {
		if !extension.timestamp.IsZero() {
			return true
		}
	}
	return false
}

// Extensions is a "container" intended to be stored in the map of Extensions
// indexed by remote address. It holds a slice of Extension "handles"
// and the Process ID and Process Group ID of the process that called an API
// method, e.g. the pid/pgid of the process at the remote address, which
// will be an Extension or a Lambda Runtime instance.
type Extensions struct {
	ExtensionSlice     // Slice of Extension "handles"
	pid            int // Process ID
	pgid           int // Process Group ID
}

type ExtensionsAPI struct {
	functionName     string                 // Required by the register method
	version          string                 // Required by the register method
	handler          string                 // Required by the register method
	extensions       map[int]ExtensionSlice // ExtensionSlice keyed by Process Group ID
	extensionsByAddr map[string]Extensions  // ExtensionSlice/pid/pgid keyed by RemoteAddr
	extensionByID    map[string]*Extension  // Extension handle keyed by Extension Identifier
	extensionPaths   []string
	timeout          int // The AWS_LAMBDA_FUNCTION_TIMEOUT value from config.
	maxConcurrency   int // The MAX_CONCURRENCY value from config.
	enabled          bool
	registered       bool // Used to record if any Extension has registered.
	sync.RWMutex
}

func NewExtensionsAPI(cfg *rapid.Config) *ExtensionsAPI {
	eapi := &ExtensionsAPI{
		functionName:     cfg.FunctionName,
		version:          cfg.Version,
		handler:          cfg.Handler,
		timeout:          cfg.Timeout,
		maxConcurrency:   cfg.MaxConcurrency,
		extensions:       make(map[int]ExtensionSlice),
		extensionsByAddr: make(map[string]Extensions),
		extensionByID:    make(map[string]*Extension),
	}

	// Check if Extensions are disabled and if not enumerate the Extension paths.
	if _, err := os.Stat(extensionsDisableFile); err == nil {
		eapi.enabled = false
		slog.Info("Extensions disabled:", slog.String("file", extensionsDisableFile))
	} else {
		eapi.enabled = true
		// Get External Extension paths (Extensions must reside in /opt/extensions)
		var paths []string
		if entries, err := os.ReadDir(extensionDir); err == nil {
			// Get the entries of the /opt/extensions dir
			for _, entry := range entries {
				// If entry is not a directory assume it's a file and add its path
				// TODO could make more robust and test it's a real executable file.
				if !entry.IsDir() {
					paths = append(paths, path.Join(extensionDir, entry.Name()))
				}
			}
		}

		if len(paths) > 0 {
			slog.Info("External Extensions available:", slog.Any("paths", paths))
			eapi.extensionPaths = paths
		}
	}

	return eapi
}

// Return a slice containing the full pathname of all External Extensions.
// If empty it means that there are no available External Extensions.
func (eapi *ExtensionsAPI) Paths() []string {
	return eapi.extensionPaths
}

// Return true if Extensions are enabled to receive events, false if not.
// Note that unless explicitly disabled by the presence of the file
// /opt/disable-extensions-jwigqn8j Extensions will start out enabled
// but if no Extensions have registered by the first invocation of the
// Runtime API then DisableEvents() is called to avoid calling Extension
// related code unnecessarily.
func (eapi *ExtensionsAPI) EventsEnabled() bool {
	return eapi.enabled
}

// Disable the Extensions API event handling code.
// Unless explicitly disabled by the file /opt/disable-extensions-jwigqn8j
// the Extensions API starts enabled, however if no Extensions have registered
// by the first invoke of the Runtime API we will call DisableEvents() to
// disable Extensions to avoid calling Extension related code unnecessarily.
//
// Note that it is possible to have extensions that simply share a lifecycle
// with a Runtime instance and do not receive events, for example Extensions
// that behave like "sidecars" and communicate with Runtimes via Extension
// specific IPC. Such Extensions will still work after the DisableEvents()
// call, which purely affects Extensions API events.
func (eapi *ExtensionsAPI) DisableEvents() {
	eapi.enabled = false
}

// Return true if any Extensions are registered, false if not.
// Note that this is explicitly intended to mean if *any* Extension has
// "ever" registered with this Runtime API Daemon. This means that if
// MAX_CONCURRENCY is set > 1 and an Extension has registered as a result of
// the first Runtime/Extension instance being invoked then this will be set
// true even if additional instances are yet to be invoked.
//
// The intended use of this method/flag is to avoid calling Extension related
// code unnecessarily. We can't just check extensionPaths for this purpose
// because it is possible, if unusual, for Internal Extensions to register.
func (eapi *ExtensionsAPI) Registered() bool {
	return eapi.registered
}

// Called by the RuntimeAPI when the ManagedProcess representing a
// Lambda Runtime instance is registered with the ProcessManager.
func (eapi *ExtensionsAPI) RegisterLambdaRuntime(pid int, nproc int) {
	eapi.Lock()
	defer eapi.Unlock()
	eapi.extensions[pid] = nil // Initialise to nil as there may be no extensions.
}

// Called by the RuntimeAPI when the ManagedProcess representing a
// Lambda Runtime instance is unregistered from the ProcessManager.
func (eapi *ExtensionsAPI) UnregisterLambdaRuntime(pid int, nproc int) {
	eapi.Lock()
	defer eapi.Unlock()

	// First iterate all Extension handles for this pid/pgid to get
	// their Extension ID and use that to delete the extensionByID entry
	// then delete the extensions entry for this pid/pgid
	extensions := eapi.extensions[pid]
	for _, extension := range extensions {
		delete(eapi.extensionByID, extension.id)
	}
	delete(eapi.extensions, pid)
}

// Add a "handle" representing the Extension with the supplied name using the
// supplied process group ID as a key so we can find all the Extensions indexed
// by a given pgid. The Extension handle represents a means to reference and
// synchronise with the *actual* Extensions, which are independent processes.
// Adding the "handle" inserts it into a map keyed by the pgid and also
// one keyed by the generated unique extension ID. This method also returns
// a reference to the newly added "handle".
func (eapi *ExtensionsAPI) Add(name string, pgid int) *Extension {
	extension := &Extension{
		awaitRegister: make(chan struct{}),
		events:        make(chan Request),
		name:          name,
		id:            uuid.Generate().String(),
	}

	eapi.Lock()
	defer eapi.Unlock()
	eapi.extensions[pgid] = append(eapi.extensions[pgid], extension)
	eapi.extensionByID[extension.id] = extension
	return extension
}

// Get the slice of Extension "handles" indexed by process group ID.
func (eapi *ExtensionsAPI) IndexedByPgid(pgid int) ExtensionSlice {
	eapi.RLock()
	defer eapi.RUnlock()
	return eapi.extensions[pgid]
}

// Get the slice of Extension "handles" and the pid and pgid of the
// Extension or Runtime process indexed by the remote address.
// e.g. the host:port of the connected Extension or Runtime process.
func (eapi *ExtensionsAPI) IndexedByAddr(addr string) (ext ExtensionSlice, pid int, pgid int) {
	eapi.RLock()
	defer eapi.RUnlock()
	if eapi.maxConcurrency == 1 {
		for pgid, ext := range eapi.extensions {
			return ext, pgid, pgid
		}
	} else if extensions, ok := eapi.extensionsByAddr[addr]; ok {
		return extensions.ExtensionSlice, extensions.pid, extensions.pgid
	}
	return nil, 0, 0
}

// Get the Extension "handle" indexed by extension ID
func (eapi *ExtensionsAPI) ExtensionByID(id string) *Extension {
	eapi.RLock()
	defer eapi.RUnlock()
	return eapi.extensionByID[id]
}

// This method is attached to the Runtime API Server ConnState callback hook
// https://pkg.go.dev/net/http#Server
// https://stackoverflow.com/questions/62766497/is-there-a-way-to-get-all-the-open-http-connections-on-a-host-in-go
// If MAX_CONCURRENCY > 1 and Extension events are enabled then this method
// provides a mapping between the address the Extension or Runtime instance
// used to call an API method and the pid of that process, from where we may
// obtain the pgid and thence the ExtensionSlice (from extensions[pgid]).
// This, and the associated IndexedByAddr() method, gives us a means to
// "demultiplex" when multiple concurrent instances are active such that
// events for a given invocation are only delivered to a given Runtime and
// its associated Extension instances (e.g. the Extension instances in the
// same process group as that Runtime instance).
// If MAX_CONCURRENCY == 1 this mapping/demultiplexing is not required as
// there will only be a single process group for the Runtime and Extensions.
// If no Extensions have registered by the first first invocation of the
// Runtime API then eapi.enabled will be set false, as Exension events are
// not required in that case and this method will then return immediately.
//
// Note that the FindTCPInodeFromAddress/GetPids/FindPidFromInode used by
// this method work by looking up the /proc virtual filesystem when new
// connections are made to the Runtime API Daemon. Well written Runtime
// and Extension applications can reuse the underlying TCP connection to
// avoid the cost of establishing connections for each API method call.
// This is a good idea for performance and latency reasons and the AWS
// Runtimes like awslambdaric for Python do this, but the AWS Example
// Extension in Python uses the requests.get requests.post global functions
// https://github.com/aws-samples/aws-lambda-extensions/tree/main/python-example-extension
// Those global functions do not reuse connections and a more efficient
// approach would be to create as session with: session = requests.Session()
// and do session.get() or session.post() calls.
func (eapi *ExtensionsAPI) MapRemoteAddrToExtensions(conn net.Conn, state http.ConnState) {
	if !eapi.enabled { // Will be set false if DisableEvents() has been called.
		return
	}
	eapi.Lock()
	defer eapi.Unlock()

	switch {
	case state == http.StateNew:
		//slog.Info("----- MapRemoteAddrToExtensions -----")
		//slog.Info(state)
		address := conn.RemoteAddr().String()
		//slog.Info(address)
		inode := process.FindTCPInodeFromAddress(address)

		// Get a slice of candidate pids from /proc, applying a filter to
		// the full list that selects only those pids whose pgid is
		// in the map of extensions keyed by pgid.
		pids := process.GetPids(func(pid int) bool {
			if pgid, err := syscall.Getpgid(pid); err == nil {
				_, ok := eapi.extensions[pgid]
				return ok
			}
			return false
		})

		pid := process.FindPidFromInode(pids, inode)
		// Having found the pid of the remote address we get its process group
		// ID and use that to find the required slice of Extension handles.
		if pgid, err := syscall.Getpgid(pid); err == nil {
			//slog.Info("-------- MATCHING PID:", slog.Int("pid", pid))
			//slog.Info("-------- PGID:", slog.Int("pgid", pgid))
			eapi.extensionsByAddr[address] = Extensions{
				eapi.extensions[pgid], pid, pgid,
			}
		}
	case state == http.StateClosed:
		// Remove extensionsByAddr mapping on http.StateClose
		address := conn.RemoteAddr().String()
		delete(eapi.extensionsByAddr, address)
	}
}

// Helper function to render error response JSON given type and message strings.
func errMsg(etype string, msg string) []byte {
	return []byte(`{"errorType": "` + etype + `", "errorMessage": "` + msg + `"}`)
}

// Handler for the AWS Lambda Extensions API register method
// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-extensions-api.html#extensions-registration-api-a
// During the Extension init phase, each extension needs to register with
// Lambda to receive events. Lambda uses the full file name of the extension to
// validate that the extension has completed the bootstrap sequence. Therefore,
// each Register API call must include the Lambda-Extension-Name header with
// the full file name of the extension.
//
// You can register up to 10 extensions for a function. This limit is enforced
// through the Register API call.
//
// After each extension registers, Lambda starts the Runtime init phase.
// https://lumigo.io/wp-content/uploads/2020/10/Picture3-How-AWS-Lambda-Extensions-change-the-Lambda-lifecycle.png
// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-extensions-api.html#runtimes-extensions-api-reg
func (eapi *ExtensionsAPI) register(w http.ResponseWriter, r *http.Request) {
	type RegisterRequest struct { // JSON body
		Events []string `json:"events"`
	}

	headers := r.Header

	// Lambda-Extension-Name should be the full file name of the extension.
	extensionName := headers.Get("Lambda-Extension-Name")
	if extensionName == "" { // Send error response to Extension
		slog.Warn("ExtensionsAPI empty extension name")
		w.WriteHeader(http.StatusForbidden)
		w.Write(errMsg("Extension.InvalidExtensionName", "Empty extension name"))
		return
	}

	// Used to specify optional Extensions features during registration.
	// Features available to specify using this setting:
	// accountId – If specified, the Extension registration response will
	//     contain the account ID associated with the Lambda function that
	//     we are registering the Extension for. TODO - not yet supported
	//extensionAcceptFeature := headers.Get("Lambda-Extension-Accept-Feature")

	var events []string
	var err error
	if r.Body == nil {
		err = errors.New("Request Body has not been set")
	} else {
		var body []byte
		body, err = io.ReadAll(r.Body)
		if err == nil {
			req := RegisterRequest{}
			if err = json.Unmarshal(body, &req); err == nil {
				events = req.Events
			}
		}
	}

	if err != nil {
		slog.Warn(
			"ExtensionsAPI failed to parse register request body:",
			slog.Any("error", err),
		)
		w.WriteHeader(http.StatusForbidden)
		w.Write(errMsg("InvalidRequestFormat", err.Error()))
		return
	}

	// Get Extensions indexed by RemoteAddr (host:port of connected Extension)
	extensionType := "External"
	extensions, _, pgid := eapi.IndexedByAddr(r.RemoteAddr)
	extension := extensions.FindByName(extensionName)
	if extension == nil { // It's likely an Internal Extension
		if eapi.enabled && !extensions.RegistrationIsClosed() {
			// Add Internal Extension "handle"
			extensionType = "Internal"
			extension = eapi.Add(extensionName, pgid)
		} else {
			errorType := "Extension.RegistrationServiceOff"
			errorMessage := fmt.Sprintf(
				"ExtensionsAPI failed to register Internal Extension %s:", extensionName)
			slog.Warn(errorMessage, slog.String("type", errorType))
			w.WriteHeader(http.StatusForbidden)
			w.Write(errMsg(errorType, errorMessage))
			return
		}
	} else if extension.registered {
		errorType := "Extension.AlreadyRegistered"
		errorMessage := fmt.Sprintf("ExtensionsAPI failed to register %s:", extensionName)
		slog.Warn(
			errorMessage,
			slog.String("id", extension.id),
			slog.String("type", errorType),
		)
		w.WriteHeader(http.StatusForbidden)
		w.Write(errMsg(errorType, errorMessage))
		return
	}

	for _, event := range events {
		if event == "INVOKE" {
			extension.invokeIsRegistered = true
		} else if event == "SHUTDOWN" && extensionType == "External" {
			extension.shutdownIsRegistered = true
		} else {
			errorType := "Extension.InvalidEventType"
			if event == "SHUTDOWN" {
				errorType = "ShutdownEventNotSupportedForInternalExtension"
			}
			errorMessage := fmt.Sprintf(
				"ExtensionsAPI Failed to Register %s: event %s", extensionName, event)
			slog.Warn(errorMessage, slog.String("type", errorType))
			w.WriteHeader(http.StatusForbidden)
			w.Write(errMsg(errorType, errorMessage))
			return
		}
	}
	extension.registered = true

	message := fmt.Sprintf(
		"%s Extension %s (%s) Registered, Subscribed to %s",
		extensionType, extensionName, extension.id, events,
	)
	slog.Info(message)

	// Closing awaitRegister signals that this extension is registered.
	close(extension.awaitRegister)

	eapi.registered = true
	// Generated unique agent ID (UUID string) required for all subsequent requests.
	w.Header().Set("Lambda-Extension-Identifier", extension.id)
	w.WriteHeader(http.StatusOK)
	response := []byte(`{` +
		`"functionName":"` + eapi.functionName + `",` +
		`"functionVersion":"` + eapi.version + `",` +
		`"handler":"` + eapi.handler + `"` +
		`}`)

	w.Write(response)
}

// Handler for the AWS Lambda Extensions API next event method
// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-extensions-api.html#extensions-api-next
// Extensions send a Next API request to receive the next event, which can be
// an Invoke event or a Shutdown event. The response body contains the payload,
// which is a JSON document that contains event data.
//
// The extension sends a Next API request to signal that it is ready to receive
// new events. This is a blocking call.
func (eapi *ExtensionsAPI) next(w http.ResponseWriter, r *http.Request) {
	// Use context.WithCancel to provide a mechanism to cancel
	// ExtensionsAPI next requests should the Extension be terminated.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel() // Ensure cancellation when handler exits.

	headers := r.Header

	// Unique identifier for the extension (UUID string).
	extensionID := headers.Get("Lambda-Extension-Identifier")
	if extensionID == "" { // Send error response to Extension
		w.WriteHeader(http.StatusForbidden)
		w.Write(errMsg("Extension.MissingExtensionIdentifier",
			"Missing Lambda-Extension-Identifier header"))
		return
	}

	// Get the Extension "handle" indexed by extensionID
	extension := eapi.ExtensionByID(extensionID)
	if extension == nil { // Send error response to Extension
		w.WriteHeader(http.StatusForbidden)
		w.Write(errMsg("Extension.InvalidExtensionIdentifier",
			"Invalid Lambda-Extension-Identifier"))
		return
	}

	select {
	case event := <-extension.events:
		extension.timestamp = time.Now()

		// Unique identifier for the event (UUID string).
		extensionEventID := uuid.Generate().String()
		w.Header().Set("Lambda-Extension-Event-Identifier", extensionEventID)
		w.WriteHeader(http.StatusOK)

		if event.ID == "SHUTDOWN" { // Shutdown event
			//  The shutdownReason includes the following values:
			//    SPINDOWN – Normal shutdown
			//    TIMEOUT – Duration limit timed out
			//    FAILURE – Error condition, such as an out-of-memory event
			deadline := time.Now().UnixMilli() + (int64)(eapi.timeout)*1000
			response := []byte(`{` +
				`"eventType":"SHUTDOWN",` +
				`"shutdownReason":"` + string(event.Body) + `",` +
				`"deadlineMs":` + strconv.Itoa(int(deadline)) +
				`}`)

			w.Write(response)
		} else { // Invoke event
			tracing := ""
			if event.XRay != "" {
				tracing = `,"tracing":{"type":"X-Amzn-Trace-Id","value":"` + event.XRay + `"}`
			}
			response := []byte(`{` +
				`"eventType":"INVOKE",` +
				`"deadlineMs":` + strconv.Itoa(int(event.Deadline)) + `,` +
				`"requestId":"` + event.ID + `",` +
				`"invokedFunctionArn":"` + event.FunctionName + `"` +
				tracing +
				`}`)

			w.Write(response)
		}
	case <-ctx.Done():
		slog.Info("Terminating Idle Extension Instance")
	}
}

// Handler for the AWS Lambda Extensions API init error method
// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-extensions-api.html#runtimes-extensions-init-error
func (eapi *ExtensionsAPI) initerror(w http.ResponseWriter, r *http.Request) {
	// TODO
}

// Handler for the AWS Lambda Extensions API exit error method
// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-extensions-api.html#runtimes-extensions-exit-error
func (eapi *ExtensionsAPI) exiterror(w http.ResponseWriter, r *http.Request) {
	// TODO
}
