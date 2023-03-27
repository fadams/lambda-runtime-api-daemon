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

package invokeapi

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus" // Structured logging
	"math"
	"time"

	// https://pkg.go.dev/github.com/docker/distribution/uuid
	"github.com/docker/distribution/uuid"

	"lambda-runtime-api-daemon/pkg/process"
	"lambda-runtime-api-daemon/pkg/runtimeapi"
)

const (
	// AWS Error response seems to use RFC3339 timestamps will millisecond precision
	// but https://pkg.go.dev/time#pkg-constants only has RFC3339 and RFC3339Nano
	RFC3339Milli = "2006-01-02T15:04:05.999Z07"
)

type InvokeAPI struct {
	close   func()
	pm      *process.ProcessManager
	rapi    *runtimeapi.RuntimeAPI
	version string
	handler string
	cwd     string
	cmd     []string
	env     []string
	timeout int
	memory  int
	report  bool
}

// Helper function to render timeout message for logging and response message.
func timeoutMessage(id string, timeout float64) string {
	return fmt.Sprintf("%s Task timed out after %.2f seconds", id, timeout)
}

// Helper function to render the timeout response JSON given a message string.
func timeoutResponse(message string) []byte {
	message = fmt.Sprintf("%s %s", time.Now().Format(RFC3339Milli), message)
	return []byte(`{"errorMessage": "` + message + `"}`)
}

// Compute the duration between the supplied start time and current time,
// clamping the computed duration to the supplied timeout if that is less than
// the computed duration, finally convert from ns to ms
func durationMS(start time.Time, timeout int) float64 {
	return math.Min(float64(time.Now().Sub(start).Nanoseconds()),
		float64(timeout*1000000000)) / float64(time.Millisecond)
}

// Invoke the named function. This method is intended to be common to all the
// invocation "triggers" (REST, AMQP-RPC, etc.). The arguments include the
// root/base Context, the function name, a correlation ID used to correlate
// requests with responses which will be used as the  Runtime API "AwsRequestId"
// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html
// a (possibly empty) base64 encoded Client Context from the calling client
// https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html#API_Invoke_RequestSyntax
// and the request body as a byte slice.
//
// This method sends the request on a channel that will block until the invoked
// function has initialised and is available to receive the request, and will
// then block again on a response channel until the invoked function responds
// whereupon this method will return with the function response as a byte slice.
// Handlers that use this method should therefore be aware of the likelihood
// that it might block, potentially for the full AWS_LAMBDA_FUNCTION_TIMEOUT.
func (iapi *InvokeAPI) invoke(rctx context.Context, name string,
	correlationID string, b64CC string, body []byte) []byte {

	log.Debugf("InvokeAPI invocations: " + string(body))
	invokeStart := time.Now()

	// Use context.WithTimeout to provide a mechanism to cancel invocation
	// requests and/or responses should the Lambda duration be exceeded.
	// https://pkg.go.dev/context#example-WithTimeout
	// https://ieftimov.com/posts/make-resilient-golang-net-http-servers-using-timeouts-deadlines-context-cancellation/
	timeout := time.Duration(iapi.timeout) * time.Second
	ctx, cancel := context.WithTimeout(rctx, timeout)
	defer cancel() // Ensure cancellation when handler exits.

	// Use supplied correlationID if set, otherwise generate one.
	if correlationID == "" {
		correlationID = uuid.Generate().String()
	}

	// Handling the ClientContext is a little "odd"/convoluted. According to
	// https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html. Up to
	// 3,583 bytes of base64-encoded data about the invoking client to pass to
	// the function in the context object. So at this point the client should
	// have encoded the object as Base64, but as it happens the Runtime
	// Interface Clients actually expect JSON *not* Base64 encoded JSON, so
	// we do the Base64 decoding here.
	clientContext := ""
	if b64CC != "" {
		if bytesCC, err := base64.StdEncoding.DecodeString(b64CC); err == nil {
			clientContext = string(bytesCC)
		} else {
			log.Errorf("Error decoding Client Context %s %s", b64CC, err)
			return []byte(`{` +
				`"errorType": "Runtime.LambdaContextUnmarshalError", ` +
				`"errorMessage": "Unable to decode base64: ` + err.Error() + `"` +
				`}`)
		}
	}

	invocation := runtimeapi.Request{
		Response:      make(chan runtimeapi.Response, 1), // Needs to buffer one item.
		FunctionName:  name,
		ID:            correlationID,
		ClientContext: clientContext,
		Body:          body,
		// The date that the function times out in Unix time milliseconds.
		// This deadline is passed to the actual Lambda being invoked as part
		// of the Lambda context so the running Lambda will know its own deadline.
		Deadline: invokeStart.UnixMilli() + (int64)(iapi.timeout)*1000,
	}

	if iapi.report {
		fmt.Println("START RequestId: " + correlationID + " Version: " + iapi.version)
	}

	initDuration := ""
	// If the invocation can be sent without blocking, or ctx.Done(), this
	// select will finish immediately. However, if writing the invocation
	// would block it means there aren't enough concurrent Lambda handlers,
	// so launch a new Lambda instance until max concurrency is reached.
	select {
	case iapi.rapi.Invocations <- invocation:
	case <-ctx.Done():
	default:
		// The RuntimeAPIServer Init references a sync.Once instance indexed by
		// the number of Lambda instances connected to the Runtime API Daemon.
		// That index starts at zero and increments as Lambda instances are
		// created. Before the instance is created the Once is "available" and
		// the Do method is used to wrap the actual init, which will launch
		// a new instance if the maximum configured concurrency hasn't been
		// reached. Any additional concurrent calls that arrive whilst
		// waiting for the new instance to init pass through (because of the
		// Once) and subsequently block on iapi.rapi.invocations <- invocation.
		// When the number of instances servicing requests is >= the required
		// concurrency this default branch of the select isn't reached.
		iapi.rapi.Init(func() {
			initStart := time.Now()
			iapi.init()
			initDurationMS := durationMS(initStart, iapi.timeout)
			initDuration = fmt.Sprintf("Init Duration: %.2f ms\t", initDurationMS)
		})

		// After launching a new Lambda instance do a blocking invocation send.
		// Select on the rapi.initError chan too to check for any Lambda Init
		// state errors and forward them to the invocation.Response chan.
		select {
		case iapi.rapi.Invocations <- invocation:
		case initErrorResponse := <-iapi.rapi.InitError:
			invocation.Response <- initErrorResponse // Forward to invocation.Response
		case <-ctx.Done():
		}
	}

	var response []byte

	select {
	case invocationResponse := <-invocation.Response:
		response = invocationResponse.Response
	case <-ctx.Done():
		// If the invocationResponse is cancelled it means that the
		// RuntimeAPI response method hasn't been called, so we need to
		// remove the pendingRequests entry to prevent a resource leak
		// if the RuntimeAPI Client has died, or an attempted write to
		// the (soon to be closed) invocation.Response channel for the
		// case where the Lambda Handler takes longer to process than
		// our invocation timeout.
		iapi.rapi.DeleteRequest(correlationID)

		// Cancellation could be due to a closed client connection,
		// so we check the error and only write timeout response if the
		// cancellation is actually due to a timeout.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			message := timeoutMessage(correlationID, float64(iapi.timeout))
			log.Info(message)
			response = timeoutResponse(message)
		}
	}

	if iapi.report {
		duration := durationMS(invokeStart, iapi.timeout)
		// TODO set the real Max Memory Used and Memory Size
		fmt.Printf(
			"END RequestId: %s\n"+
				"REPORT RequestId: %s\t%sDuration: %.2f ms\t"+
				"Billed Duration: %.f ms\t"+
				"Memory Size: %d MB\tMax Memory Used: %d MB\t\n",
			correlationID, correlationID, initDuration, duration,
			math.Ceil(duration), iapi.memory, iapi.memory)
	}
	return response
}

func (iapi *InvokeAPI) init() {
	log.Infof("exec '%s' (cwd=%s, handler=%s)", iapi.cmd[0], iapi.cwd, iapi.handler)

	cmd := iapi.pm.NewManagedProcess(iapi.cmd, iapi.cwd, iapi.env)
	err := cmd.Start()
	if err != nil {
		log.Warnf("Failed to start %v with error %v", cmd, err)
	}
}

// Cleanly close the InvokeAPI. Delegates to a concrete implementation that
// is assigned by the implementation specific factory method.
func (iapi *InvokeAPI) Close() {
	iapi.close()
}
