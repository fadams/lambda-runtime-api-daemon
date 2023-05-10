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
	//"fmt"
	log "github.com/sirupsen/logrus" // Structured logging
	"io"
	"net/http"
	"path"
	"strconv"
	"sync"
	// According to https://pkg.go.dev/syscall the syscall package is deprecated
	// and callers should use the corresponding package in the golang.org/x/sys
	// repository instead. See https://golang.org/s/go1.4-syscall for more info.
	syscall "golang.org/x/sys/unix"
	"time"

	// Chose chi over github.com/gorilla/mux as it seems the more active project
	// https://pkg.go.dev/github.com/go-chi/chi/v5
	"github.com/go-chi/chi/v5"

	"lambda-runtime-api-daemon/pkg/process"
)

type Request struct {
	Response      chan Response // Response channel
	FunctionName  string        // Name of the Lambda function, version, or alias.
	ID            string        // Correlation ID
	ClientContext string        // base64 data about invoking client passed to Lambda
	XRay          string        // The AWS X-Ray Tracing Header from the invocation
	Body          []byte        // Raw invocation request Payload
	Deadline      int64         // Date the function expires in Unix time milliseconds
}

type Response struct {
	Response []byte
}

type RuntimeAPI struct {
	close           func()
	pm              *process.ProcessManager
	extensions      *ExtensionsAPI
	Invocations     chan Request             // chan of requests sent by Invoke API
	InitError       chan Response            // Lambda initerror Response channel
	pendingRequests map[string]chan Response // Maps request ID to response chan
	timestamp       time.Time                // Timestamp when all Lambdas are active
	shutdown        chan struct{}            // chan to shutdown inactive Lambdas
	initialisers    []sync.Once              // Each Lambda may be initialised once
	idleTimeout     int                      // Reclaim Idle instances after this
	lambdaCount     int                      // Count of Lambda instances
	sync.RWMutex
}

// Init references a sync.Once instance indexed by the number of Lambda
// instances connected to the Runtime API Daemon. That index starts at zero and
// increments as Lambda instances are created. Before the instance is created
// the Once is "available" and the Do method is used to wrap the actual init
// such that it is only callable once.
func (rapi *RuntimeAPI) Init(init func()) {
	rapi.RLock()

	index := rapi.lambdaCount
	bound := len(rapi.initialisers) - 1
	if index > bound { // Clamp index to upper bound of initialisers slice
		index = bound
	}
	// Need to get the current initialiser's sync.Once by reference to
	// avoid attempting to copy its internal lock value.
	once := &rapi.initialisers[index]

	rapi.RUnlock() // Avoid holding the lock during the actual init call

	once.Do(init)
}

// Called by the ProcessManager when the ManagedProcess representing a
// Lambda Runtime instance is registered with the ProcessManager.
func (rapi *RuntimeAPI) RegisterLambdaRuntime(pid int, nproc int) {
	rapi.extensions.RegisterLambdaRuntime(pid, nproc)

	rapi.lambdaCount = nproc
	rapi.timestamp = time.Now()
}

// Called by the ProcessManager when the ManagedProcess representing a
// Lambda Runtime instance is unregistered from the ProcessManager.
func (rapi *RuntimeAPI) UnregisterLambdaRuntime(pid int, nproc int) {
	rapi.extensions.UnregisterLambdaRuntime(pid, nproc)

	// When a Lambda Runtime is unregistered we reset the sync.Once of its
	// initialiser so that should a new Lambda instance be required again
	// at some later date its sync.Once protected initialiser can fire.
	rapi.initialisers[nproc] = sync.Once{}
	rapi.lambdaCount = nproc
}

// Explicitly delete an outstanding request. In general requests are implicitly
// deleted when the associated invocation response/error occurs, however if
// the invocaion is cancelled due to a timeout or closed connection it requires
// a mechanism to delete the outstanding request.
func (rapi *RuntimeAPI) DeleteRequest(id string) {
	rapi.Lock()
	defer rapi.Unlock()
	delete(rapi.pendingRequests, id)
}

// Return the ExtensionsAPI reference.
func (rapi *RuntimeAPI) Extensions() *ExtensionsAPI {
	return rapi.extensions
}

// Handler for the AWS Lambda Runtime API next invocation method
// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html#runtimes-api-next
func (rapi *RuntimeAPI) next(w http.ResponseWriter, r *http.Request) {
	// Nested funtion called on idle timeout to send the shutdown event to
	// Extensions that have registered to receive it and then terminate.
	terminateIdleInstance := func() {
		if rapi.extensions.Registered() {
			// For a function with registered extensions, Lambda supports
			// graceful shutdown. This means that External Extensions are
			// sent shutdown events if they have registered to receive
			// them and SIGTERM will be sent to Extensions and also the
			// Runtime. For Internal Extensions the documentation is not
			// very clear, but they *can* register, but (weirdly) they are
			// not actually allowed to register for SHUTDOWN, however if
			// {"events": []} is sent to register then the Runtime will
			// be sent SIGTERM. Unfortunately this does not work on Python 3.8
			// and 3.9 runtimes (see link below) as they use native C++ code
			// to service the "next" API requests which blocks signals
			// from being propagated to the Python interpreter.
			// https://github.com/aws-samples/graceful-shutdown-with-aws-lambda

			// Get Extensions indexed by RemoteAddr (host:port of connected Runtime)
			extensions, pid, _ := rapi.extensions.IndexedByAddr(r.RemoteAddr)
			// Send Shutdown Event to Extensions that have registered to receive it.
			extensions.SendShutdownEvent("SPINDOWN")

			// From the Extensions API documentation:
			// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-extensions-api.html#runtimes-lifecycle-shutdown
			// The maximum duration of the Shutdown phase depends on the
			// configuration of registered extensions:
			//     0 ms – A function with no registered extensions
			//     500 ms – A function with a registered internal extension
			//     2,000 ms – A function with one or more registered external extensions
			log.Infof("Terminating idle Lambda Runtime pid: %d", pid)
			if len(rapi.extensions.Paths()) > 0 { // Registered External Extensions
				time.Sleep(1700 * time.Millisecond)
				syscall.Kill(pid, syscall.SIGTERM)
				time.Sleep(300 * time.Millisecond)
			} else { // Registered Internal Extensions only
				syscall.Kill(pid, syscall.SIGTERM)
				time.Sleep(500 * time.Millisecond)
			}
			// if the Runtime/Extension does not respond to the Shutdown event
			// within the limit, Lambda ends the Runtime and Extensions by
			// sending a SIGKILL signal to the *process group* (hence the -pid).
			syscall.Kill(-pid, syscall.SIGKILL)
		} else {
			// If no Extensions are registered Lambda ends Runtime with SIGKILL.
			if pid := rapi.pm.FindPidFromAddress(r.RemoteAddr); pid > 0 {
				log.Infof("Terminating idle Lambda Runtime pid: %d", pid)
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}

	// Use context.WithTimeout to provide a mechanism to cancel Lambda next
	// requests should the Lambda idle timeout be exceeded.
	// https://pkg.go.dev/context#example-WithTimeout
	// https://ieftimov.com/posts/make-resilient-golang-net-http-servers-using-timeouts-deadlines-context-cancellation/
	ctx, cancel := context.WithTimeout(
		r.Context(), time.Duration(rapi.idleTimeout)*time.Second)
	defer cancel() // Ensure cancellation when handler exits.

	select {
	case invocation := <-rapi.Invocations:
		// If Extensions have registered for invoke events forward invocation.
		// Note that according the AWS Extensions API documentation:
		// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-extensions-api.html#runtimes-lifecycle-invoke
		// During the invocation, external extensions run in parallel with
		// the function. They also continue running after the function has
		// completed. After receiving the function response from the
		// runtime, Lambda returns the response to the client, **even if
		// extensions are still running**. That statement is confirmed here:
		// https://lumigo.io/blog/lambda-extensions-just-got-even-better/
		if rapi.extensions.Registered() {
			// Get Extensions indexed by RemoteAddr (host:port of connected Runtime)
			extensions, _, _ := rapi.extensions.IndexedByAddr(r.RemoteAddr)
			extensions.SendInvokeEvent(invocation)
		} else {
			rapi.extensions.DisableEvents()
		}

		correlationID := invocation.ID
		deadline := strconv.FormatInt(invocation.Deadline, 10)

		rapi.Lock()
		// Don't use defer rapi.Unlock() here as we want critical section small.
		// Store the Response channel passed in the invocation Request
		// in a map indexed (in a concurrent-safe way) by correlationID.
		rapi.pendingRequests[correlationID] = invocation.Response

		// If Lambdas are inactive they will be terminated. Where no requests
		// at all arrive this is straightforward and simply handled by the
		// context.WithTimeout. A more complex case is where the number of
		// instances has grown to cater for concurrent requests, but then the
		// concurrency requirement has reduced. In that scenario requests will
		// be "load balanced" across the available Lambdas and the context
		// will likely not timeout. To cater for this we have an "active"
		// timestamp that will be set to the current time each time the
		// number of pending requests equals the available concurrency (and
		// thus no idle instances). Where the number of pending requests is
		// less than the available concurrency that timestamp is not refreshed
		// and the time since the last refresh will eventually exceed the
		// idle timeout, at which point the shutdown channel will be signalled.
		if len(rapi.pendingRequests) == rapi.lambdaCount {
			//fmt.Println("len(rapi.pendingRequests): ", len(rapi.pendingRequests))
			rapi.timestamp = time.Now()
		}
		diff := int(time.Since(rapi.timestamp).Seconds())
		//fmt.Println("diff:", diff)
		if diff > rapi.idleTimeout {
			rapi.timestamp = time.Now()
			// Notify the shutdown channel with a non-blocking send.
			select {
			case rapi.shutdown <- struct{}{}:
			default: // Make send on rapi.shutdown chan non-blocking
			}
		}
		rapi.Unlock()

		w.Header().Set("Lambda-Runtime-Aws-Request-Id", correlationID)
		w.Header().Set("Lambda-Runtime-Deadline-Ms", deadline)
		// TODO currently set to the FunctionName as passed to the InvokeAPI
		// https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html
		// According to that API FunctionName could be name only, name with
		// alias/version, a full ARN or partial ARN and it's not clear if
		// this response should just be the value as supplied or if it should
		// actually be converted to a full ARN. The Runtime API docs
		// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html#runtimes-api-next
		// say: The ARN of the Lambda function, version, or alias that's
		// specified in the invocation which implies it's just passed
		// through from the invocation, but need to chack.
		w.Header().Set("Lambda-Runtime-Invoked-Function-Arn", invocation.FunctionName)
		if invocation.XRay != "" {
			// N.B. This represents the AWS X-Ray tracing header
			// https://docs.aws.amazon.com/xray/latest/devguide/xray-concepts.html#xray-concepts-tracingheader
			// passed through from the invocation (if present).
			// See comments in invokeapiserver.go about usage of the
			// AWS X-Ray Tracing Header and approaches for propagating
			// Open Telemetry spans via X-Ray.
			w.Header().Set("Lambda-Runtime-Trace-Id", invocation.XRay)
		}
		// The InvokeAPI ClientContext is Base64 encoded, but Runtime
		// Interface Clients actually expect JSON *not* Base64 encoded JSON,
		// so invocation.ClientContext, if present, has been Base64 decoded
		// by the RAPIInvoker invoke method.
		if invocation.ClientContext != "" {
			w.Header().Set("Lambda-Runtime-Client-Context", invocation.ClientContext)
		}
		// w.Header().Set("Lambda-Runtime-Cognito-Identity", TODO)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(invocation.Body)
	case <-ctx.Done():
		terminateIdleInstance()
	case <-rapi.shutdown:
		terminateIdleInstance()
	}
}

// Handler for the AWS Lambda Runtime API invocation response method
// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html#runtimes-api-response
// Also used as handler for the AWS Runtime API invocation error method
// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html#runtimes-api-invokeerror
func (rapi *RuntimeAPI) response(w http.ResponseWriter, r *http.Request) {
	// This will be either response or error depending on which API call
	// triggered this method. Most of the required behaviour is identical
	// for both, but logs reflect the API call that was used.
	apiMethod := path.Base(r.RequestURI)

	correlationID := chi.URLParam(r, "id")

	rapi.Lock()
	// Don't use defer rapi.Unlock() here as we want critical section small.
	// Concurrent-safe lookup the invocation Response channel then delete entry.
	if responses, ok := rapi.pendingRequests[correlationID]; ok { // Do lookup
		delete(rapi.pendingRequests, correlationID) // Delete entry if present.
		// Only hold the lock for as long as necessary.
		rapi.Unlock()
		w.WriteHeader(http.StatusAccepted)

		if r.Body != nil {
			body, _ := io.ReadAll(r.Body)
			response := Response{}
			response.Response = body
			responses <- response
		}
	} else {
		rapi.Unlock() // Need explicit Unlock here.
		log.Debugf("RuntimeAPI %s RequestURI: %v has no matching requestor",
			apiMethod, r.RequestURI)
	}
}

// Handler for the AWS Lambda Runtime API Initialization error method
// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html#runtimes-api-initerror
func (rapi *RuntimeAPI) initerror(w http.ResponseWriter, r *http.Request) {
	// When this case occurs the Lambda instance will exit. The Python Lambda
	// Runtime Interface Client code does sys.exit(1) after POSTing initerror.
	// https://github.com/aws/aws-lambda-python-runtime-interface-client/blob/main/awslambdaric/bootstrap.py#L390
	w.WriteHeader(http.StatusAccepted)
	if r.Body != nil {
		body, _ := io.ReadAll(r.Body)
		response := Response{}
		response.Response = body

		// Send non-blocking response to the initError chan, which is buffered
		// with a capacity of one. This ensures the response will be available
		// even if the Lambda has been manually started for test purposes
		// rather than being launched automatically in response to invocation.
		select {
		case rapi.InitError <- response:
		default: // Make send on rapi.initError chan non-blocking
		}
	}
}

// Cleanly close the RuntimeAPI. Delegates to a concrete implementation that
// is assigned by the implementation specific factory method.
func (rapi *RuntimeAPI) Close() {
	rapi.close()
}
