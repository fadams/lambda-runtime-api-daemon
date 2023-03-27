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
	Body          []byte        // Raw invocation request Payload
	Deadline      int64         // Date the function expires in Unix time milliseconds
}

type Response struct {
	Response []byte
}

type RuntimeAPI struct {
	close           func()
	pm              *process.ProcessManager
	Invocations     chan Request             // chan of requests sent by Invoke API
	InitError       chan Response            // Lambda initerror Response channel
	pendingRequests map[string]chan Response // Maps request ID to response chan
	timestamp       time.Time                // Timestamp when all Lambdas are active
	reset           chan struct{}            // chan to reset passive Lambdas
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

func (rapi *RuntimeAPI) RegisterLambda(pid int, nproc int) {
	//log.Printf("RegisterLambda %d %d", pid, nproc)

	rapi.lambdaCount = nproc
	rapi.timestamp = time.Now()
}

func (rapi *RuntimeAPI) UnregisterLambda(pid int, nproc int) {
	//log.Printf("UnregisterLambda %d %d", pid, nproc)

	// When a Lambda is unregistered we reset the sync.Once of its
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

// Handler for the AWS Lambda Runtime API next invocation method
// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html#runtimes-api-next
func (rapi *RuntimeAPI) next(w http.ResponseWriter, r *http.Request) {
	// Use context.WithTimeout to provide a mechanism to cancel Lambda next
	// requests should the Lambda idle timeout be exceeded.
	// https://pkg.go.dev/context#example-WithTimeout
	// https://ieftimov.com/posts/make-resilient-golang-net-http-servers-using-timeouts-deadlines-context-cancellation/
	ctx, cancel := context.WithTimeout(
		r.Context(), time.Duration(rapi.idleTimeout)*time.Second)
	defer cancel() // Ensure cancellation when handler exits.

	select {
	case invocation := <-rapi.Invocations:
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
		// idle timeout, at which point the reset channel will be signalled.
		if len(rapi.pendingRequests) == rapi.lambdaCount {
			//fmt.Println("len(rapi.pendingRequests): ", len(rapi.pendingRequests))
			rapi.timestamp = time.Now()
		}
		diff := int(time.Since(rapi.timestamp).Seconds())
		//fmt.Println("diff:", diff)
		if diff > rapi.idleTimeout {
			rapi.timestamp = time.Now()
			// Notify the reset channel with a non-blocking send.
			select {
			case rapi.reset <- struct{}{}:
			default: // Make send on rapi.reset chan non-blocking
			}
		}
		rapi.Unlock()

		log.Debugf("RuntimeAPI next ID: " + correlationID)

		w.Header().Set("Lambda-Runtime-Aws-Request-Id", correlationID)
		w.Header().Set("Lambda-Runtime-Deadline-Ms", deadline)
		w.Header().Set("Lambda-Runtime-Invoked-Function-Arn", invocation.FunctionName)
		// w.Header().Set("Lambda-Runtime-Trace-Id", TODO) // N.B. X-Ray not OpenTracing
		if invocation.ClientContext != "" {
			w.Header().Set("Lambda-Runtime-Client-Context", invocation.ClientContext)
		}
		// w.Header().Set("Lambda-Runtime-Cognito-Identity", TODO)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		log.Debugf("RuntimeAPI next Body: " + string(invocation.Body))
		w.Write(invocation.Body)
	case <-ctx.Done():
		log.Info("Terminating idle Lambda instance")
	case <-rapi.reset:
		log.Info("Terminating idle Lambda instance")
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

	// The host:port of the connected Lambda Runtime Interface Client
	//fmt.Println(r.RemoteAddr)

	correlationID := chi.URLParam(r, "id")
	log.Debugf("RuntimeAPI %s id: %s", apiMethod, correlationID)

	rapi.Lock()
	// Don't use defer rapi.Unlock() here as we want critical section small.
	// Concurrent-safe lookup the invocation Response channel then delete entry.
	if responses, ok := rapi.pendingRequests[correlationID]; ok { // Do lookup
		delete(rapi.pendingRequests, correlationID) // Delete entry if present.
		// Only hold the lock for as long as necessary.
		rapi.Unlock()
		w.WriteHeader(http.StatusAccepted)
		log.Debugf("RuntimeAPI %s Request URI: %s", apiMethod, r.RequestURI)

		if r.Body != nil {
			body, _ := io.ReadAll(r.Body)
			log.Debugf("RuntimeAPI %s Response: %s", apiMethod, string(body))

			response := Response{}
			response.Response = body
			responses <- response
		}
	} else {
		rapi.Unlock() // Need explicit Unlock here.
		log.Infof("RuntimeAPI %s RequestURI: %v has no matching requestor",
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
