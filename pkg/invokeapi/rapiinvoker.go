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
	"path"
	"time"

	// https://pkg.go.dev/github.com/docker/distribution/uuid
	"github.com/docker/distribution/uuid"

	"lambda-runtime-api-daemon/pkg/config/rapid"
	"lambda-runtime-api-daemon/pkg/process"
	"lambda-runtime-api-daemon/pkg/runtimeapi"
)

const (
	// AWS Error response seems to use RFC3339 timestamps will millisecond precision
	// but https://pkg.go.dev/time#pkg-constants only has RFC3339 and RFC3339Nano
	RFC3339Milli = "2006-01-02T15:04:05.999Z07"
)

type RAPIInvoker struct {
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

func NewRAPIInvoker(cfg *rapid.Config, pm *process.ProcessManager,
	rapi *runtimeapi.RuntimeAPI) *RAPIInvoker {

	invoker := &RAPIInvoker{
		pm:      pm,
		rapi:    rapi,
		version: cfg.Version,
		handler: cfg.Handler,
		cwd:     cfg.Cwd,
		cmd:     cfg.Cmd,
		env:     cfg.Env,
		timeout: cfg.Timeout,
		memory:  cfg.Memory,
		report:  cfg.Report,
	}

	return invoker
}

// Helper function to render timeout message for logging and response message.
func timeoutMessage(id string, timeout float64) string {
	return fmt.Sprintf("%s Task timed out after %.2f seconds", id, timeout)
}

// Helper function to render the timeout response JSON given a message string.
func timeoutResponse(message string) []byte {
	message = fmt.Sprintf("%s %s", time.Now().Format(RFC3339Milli), message)
	return []byte(`{"errorType": "TimeoutError", "errorMessage": "` + message + `"}`)
}

// Compute the duration between the supplied start time and current time,
// clamping the computed duration to the supplied timeout if that is less than
// the computed duration, finally convert from ns to ms
func durationMS(start time.Time, timeout int) float64 {
	return math.Min(float64(time.Now().Sub(start).Nanoseconds()),
		float64(timeout*1000000000)) / float64(time.Millisecond)
}

// Invoke the named Function. This method is intended to be common to all the
// invocation "triggers" (REST, AMQP-RPC, etc.).
//
// invoke takes as arguments a root Context, an optional timestamp (if
// not supplied invoke should generate one), the Function name, an
// optional correlation ID (if not supplied invoke should generate one),
// an optional base64 client context as specified in the AWS Lambda
// Invoke API documentation:
// https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html#API_Invoke_RequestSyntax
// the AWS X-Ray Tracing Header from the invocation (if present)
// https://docs.aws.amazon.com/xray/latest/devguide/xray-concepts.html#xray-concepts-tracingheader and finally, invoke takes the invocation body as a byte slice.
//
// This method sends the request on a channel that will block until the invoked
// Function has initialised and is available to receive the request, and will
// then block again on a response channel until the invoked Function responds
// whereupon this method will return with the Function response as a byte slice.
//
// Handlers that use this method should therefore be aware of the likelihood
// that it might block, potentially for the full AWS_LAMBDA_FUNCTION_TIMEOUT.
func (invoker *RAPIInvoker) invoke(
	rctx context.Context,
	timestamp time.Time,
	name string,
	cid string,
	b64CC string, // Base64 Client Context
	xray string, // The AWS X-Ray Tracing Header from the invocation
	body []byte,
) []byte {
	invokeStart := timestamp
	if invokeStart.IsZero() {
		invokeStart = time.Now()
	}

	// Use context.WithTimeout to provide a mechanism to cancel invocation
	// requests and/or responses should the Lambda duration be exceeded.
	// https://pkg.go.dev/context#example-WithTimeout
	// https://ieftimov.com/posts/make-resilient-golang-net-http-servers-using-timeouts-deadlines-context-cancellation/
	timeout := time.Duration(invoker.timeout) * time.Second
	ctx, cancel := context.WithTimeout(rctx, timeout)
	defer cancel() // Ensure cancellation when handler exits.

	// Use supplied correlationID if set, otherwise generate one.
	if cid == "" {
		cid = uuid.Generate().String()
	}

	// Handling the ClientContext is a little "odd"/convoluted. According to
	// https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html. Up to
	// 3,583 bytes of base64-encoded data about the invoking client to pass to
	// the Function in the context object. So at this point the client should
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
		ID:            cid,
		ClientContext: clientContext,
		XRay:          xray,
		Body:          body,
		// The date that the Function times out in Unix time milliseconds.
		// This deadline is passed to the actual Lambda being invoked as part
		// of the Lambda context so the running Lambda will know its own deadline.
		Deadline: invokeStart.UnixMilli() + (int64)(invoker.timeout)*1000,
	}

	if invoker.report {
		fmt.Println("START RequestId: " + cid + " Version: " + invoker.version)
	}

	initDuration := ""
	// If the invocation can be sent without blocking, or ctx.Done(), this
	// select will finish immediately. However, if writing the invocation
	// would block it means there aren't enough concurrent Lambda Runtimes,
	// so launch a new Lambda instance until max concurrency is reached.
	select {
	case invoker.rapi.Invocations <- invocation:
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
		// Once) and subsequently block on invoker.rapi.invocations <- invocation.
		// When the number of instances servicing requests is >= the required
		// concurrency this default branch of the select isn't reached.
		invoker.rapi.Init(func() {
			initStart := time.Now()
			invoker.init()
			initDurationMS := durationMS(initStart, invoker.timeout)
			initDuration = fmt.Sprintf("Init Duration: %.2f ms", initDurationMS)
		})

		// After launching a new Lambda instance do a blocking invocation send.
		// Select on the rapi.initError chan too to check for any Lambda Init
		// state errors and forward them to the invocation.Response chan.
		select {
		case invoker.rapi.Invocations <- invocation:
		case initErrorResponse := <-invoker.rapi.InitError:
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
		invoker.rapi.DeleteRequest(cid)

		// Cancellation could be due to a closed client connection,
		// so we check the error and only write timeout response if the
		// cancellation is actually due to a timeout.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			message := timeoutMessage(cid, float64(invoker.timeout))
			log.Info(message)
			response = timeoutResponse(message)
		}
	}

	if invoker.report {
		duration := durationMS(invokeStart, invoker.timeout)
		// TODO set the real Max Memory Used and Memory Size
		fmt.Printf(
			"END RequestId: %s\n"+
				"REPORT RequestId: %s\t%s \tDuration: %.2f ms\t"+
				"Billed Duration: %.f ms\t"+
				"Memory Size: %d MB\tMax Memory Used: %d MB\t\n",
			cid, cid, initDuration, duration,
			math.Ceil(duration), invoker.memory, invoker.memory)
	}
	return response
}

func (invoker *RAPIInvoker) init() {
	log.Infof("exec '%s' (cwd=%s, handler=%s)", invoker.cmd[0], invoker.cwd, invoker.handler)

	// Start the Lambda Runtime Process
	// Note that as per the AWS Extensions API documentation:
	// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-extensions-api.html#runtimes-extensions-api-reg
	// After each extension registers, Lambda starts the Runtime init phase.
	// https://lumigo.io/wp-content/uploads/2020/10/Picture3-How-AWS-Lambda-Extensions-change-the-Lambda-lifecycle.png
	// So, the Runtime init phase starts *after* all the Extensions have
	// registered, however we want Extensions to run in the same process group
	// as the Runtime process so we must start that first to get its pid.
	// Therefore, if External Extensions are enabled we will stop the Runtime
	// process until the Extensions have registered then continue it afterwards.
	runtime := invoker.pm.NewManagedProcess(invoker.cmd,
		invoker.cwd, invoker.env, 0)
	if err := runtime.Start(); err != nil {
		log.Warnf("Failed to start %v with error %v", runtime, err)
	}

	// As per comment above, the pid of Runtime Process is the process group ID.
	pgid := runtime.Pid()

	// Start External Extensions, if any, which are run as independent processes.
	eapi := invoker.rapi.Extensions()
	if len(eapi.Paths()) > 0 {
		runtime.Stop() // Pause Runtime process until Extensions have registered.

		for _, p := range eapi.Paths() {
			extension := invoker.pm.NewManagedProcess([]string{p},
				invoker.cwd, invoker.env, pgid)
			if err := extension.StartUnmanaged(); err == nil {
				eapi.Add(path.Base(extension.Cmd.Path), pgid)
			} else {
				log.Warnf("Failed to start %v with error %v", extension, err)
			}
		}

		// Block until External Extensions signal that they have registered.
		eapi.IndexedByPgid(pgid).AwaitExtensionRegistration()

		runtime.Continue() // Resume Runtime process.
	}
}

func (invoker *RAPIInvoker) Close() {
	// RAPIInvoker doesn't need any additional clean up on exit.
}
