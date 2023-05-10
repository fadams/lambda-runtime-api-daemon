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
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus" // Structured logging
	"os"
	"runtime"
	"sync"
	"time"

	// https://pkg.go.dev/github.com/docker/distribution/uuid
	"github.com/docker/distribution/uuid"

	"lambda-runtime-api-daemon/pkg/config/server"
	"lambda-runtime-api-daemon/pkg/messaging"
)

type RPCInvoker struct {
	close           func()
	pendingRequests map[string]chan []byte // Maps request ID to response chan
	producer        messaging.Producer
	replyTo         string
	timeout         int
	sync.Mutex
}

func NewRPCInvoker(cfg *server.Config) *RPCInvoker {
	invoker := &RPCInvoker{
		close:           func() {}, // NOOP default implementation
		pendingRequests: make(map[string]chan []byte),
		timeout:         cfg.Timeout,
	}

	go func() {
		defer os.Exit(1) // Exiting this goroutine is fatal, so force os.Exit

		ctx, cancel := context.WithCancel(context.Background())

		// Concrete close implementation calls cancel() on the base context.
		invoker.close = func() {
			cancel()
			runtime.Goexit() // Let cancel() cleanly stop the RPCInvoker and run defers.
		}

		exitOnError := func(err error, msg string) {
			if err != nil {
				log.Infof("%s: %s", msg, err)
				// Use runtime.Goexit() https://pkg.go.dev/runtime#Goexit with
				// defer os.Exit(1) is an idiom that allows us to exit but
				// still run all the other defers, so is cleaner than just
				// directly calling os.Exit(1) here.
				// https://www.golinuxcloud.com/golang-handle-defer-calls-os-exit/#Method-3_Handle_defer_calls_with_the_osExit_and_the_Goexit
				runtime.Goexit()
			}
		}

		// Establish connection to AMQP broker. This may block if the connection
		// URL has been configured with multiple Connection Attempts. We use
		// separate connections for sending RPC requests and receiving the
		// corresponding RPC responses because it mitigates potential TCP
		// pushback on publishing affecting consumer throughput.
		// TL;DR separate connections increases throughput.
		// amqp://guest:guest@localhost:5672?connection_attempts=20&retry_delay=10&heartbeat=0
		cConnection, err := messaging.NewConnection(
			cfg.RPCServerURI,
			messaging.Context(ctx),
			messaging.ConnectionName("RPC consumer Connection"),
		)
		exitOnError(err, "RPC consumer Connection failed to connect to AMQP broker")
		defer cConnection.Close()

		pConnection, err := messaging.NewConnection(
			cfg.RPCServerURI,
			messaging.Context(ctx),
			messaging.ConnectionName("RPC producer Connection"),
		)
		exitOnError(err, "RPC producer Connection failed to connect to AMQP broker")
		defer pConnection.Close()

		cSession, err := cConnection.Session(
			messaging.AutoAck, messaging.SessionName("RPC consumer Session"))
		exitOnError(err, "Failed to open RPC consumer Session")
		defer cSession.Close()

		pSession, err := pConnection.Session(messaging.SessionName("RPC producer Session"))
		exitOnError(err, "Failed to open RPC producer Session")
		defer pSession.Close()

		// Create Consumer for AMQP RPC responses. The "" address means use a
		// broker created queue which has a unique name that we set as replyTo.
		consumer, err := cSession.Consumer("")
		exitOnError(err, "Failed to create RPC Consumer") // Should be unlikely
		invoker.replyTo = consumer.Name()                 // Get name of replyTo queue

		// Set Consumer message prefetch/capacity/QoS
		consumer.SetCapacity(100)

		// Create Producer for AMQP RPC requests. The "" address means use the
		// default direct exchange and the Message Subject will be used as the
		// Routing Key, which in this case will be the Function name.
		producer, err := pSession.Producer("")
		exitOnError(err, "Failed to create RPC Producer") // Should be unlikely
		invoker.producer = producer

		rpcResponseHandler := func(message messaging.Message) {
			cid := message.CorrelationID

			invoker.Lock()
			// Don't use defer invoker.Unlock() as we want critical section small.
			// Concurrent-safe lookup the responses channel then delete entry.
			if responses, ok := invoker.pendingRequests[cid]; ok { // Do lookup
				delete(invoker.pendingRequests, cid) // Delete entry if present.
				// Only hold the lock for as long as necessary.
				invoker.Unlock()
				responses <- message.Body
			} else {
				invoker.Unlock() // Need explicit Unlock here.
				log.Debugf("RPCInvoker Request: %v has no matching requestor", cid)
			}
		}

		// Get the Message Consume, Return, and Connection CloseNotify channels
		// outside the loop as Consume() employs a lock and CloseNotify()
		// creates the chan, registers it as a listener then returns the chan,
		// which we don't want to do each iteration.
		msgs := consumer.Consume()
		returns := producer.Return()
		pCloseNotify := cConnection.CloseNotify()
		cCloseNotify := pConnection.CloseNotify()
	loop:
		for {
			select {
			// Handle Messages from the Consumer channel
			case m := <-msgs:
				// Handler launched as goroutine to enable concurrent responses.
				go rpcResponseHandler(m)
			case m := <-returns:
				// Unroutable Messages are returned by the broker when the
				// Mandatory flag is set on published Messages. This gives us
				// a way to determine quickly if a given Function has not
				// been deployed, as its request queue won't exist.
				// Replace Message Body with an error message and reuse
				// the rpcResponseHandler.
				message := fmt.Sprintf("%s is unreachable", m.Subject)
				log.Warn(message)
				m.Body = []byte(`{"errorType": "ResourceNotFoundException",` +
					` "errorMessage": "` + message + `"}`)
				go rpcResponseHandler(m)
			// Listen on the CloseNotify channels and exit on error if broker
			// connection fails and the maximum Connection Attempts is exceeded.
			case err := <-pCloseNotify:
				exitOnError(err, "RPCInvoker consumer Connection to AMQP broker was "+
					"closed and maximum Connection Attempts exceeded")
			case err := <-cCloseNotify:
				exitOnError(err, "RPCInvoker producer Connection to AMQP broker was "+
					"closed and maximum Connection Attempts exceeded")
			case <-ctx.Done(): // Exit main loop when cancel function is called.
				break loop
			}
		}

		log.Info("RPCInvoker stopped")
	}()

	return invoker
}

// Invoke the named Function via AMQP-RPC over the messaging fabric.
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
// This method sends the request by publishing an AMQP message with the
// RoutingKey (Message Subject) set to the Function name. The method then
// creates a response channel and stores it in a map keyed by correlationID
// and blocks on the response channel until the invoked Function responds.
// The AMQP response is handled by rpcResponseHandler, which uses the
// response Message correlationID to look up the required response channel
// and then sends the Message Body to that channel, whereupon this method
// will return with the Function response as a byte slice.
//
// Handlers that use this method should therefore be aware of the likelihood
// that it might block, potentially for the full AWS_LAMBDA_FUNCTION_TIMEOUT.
func (invoker *RPCInvoker) invoke(
	rctx context.Context,
	timestamp time.Time,
	name string,
	cid string,
	b64CC string, // Base64 Client Context
	xray string, // The AWS X-Ray Tracing Header from the invocation
	body []byte,
) []byte {
	// This timestamp is set here and passed in the AMQP-RPC request message
	// Timestamp. The Lambda Runtime API Daemon will use the RPC Timestamp
	// if set, which will allow any delays due to the request queue growing
	// too large to be factored into the overall Function timeout. In most
	// cases the additional latency due to the AMQP transport should be
	// very low, but under heavy load the request queues may grow, so it is
	// useful for the Function timeout to reflect when the request was sent.
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

	rpcRequest := messaging.Message{
		Subject:       name,
		ContentType:   "application/json",
		ReplyTo:       invoker.replyTo,
		CorrelationID: cid,
		Timestamp:     invokeStart,
		Body:          []byte(body),
		Mandatory:     true, // Return unroutable Messages
		//Durable:     true,
	}
	// The Lambda Server's RPCInvoker just passes the b64CC directly to the
	// Lambda Runtime API Daemon, storing it in the X-Amz-Client-Context AMQP
	// header for consistency with the HTTP header used in the AWS Invoke API.
	// Similarly, if the xray tracing arg is set the RPCInvoker just passes
	// it through using the X-Amzn-Trace-Id AMQP header for consistency.
	if b64CC != "" && xray != "" {
		rpcRequest.Headers = map[string]interface{}{
			"X-Amz-Client-Context": b64CC,
			"X-Amzn-Trace-Id":      xray,
		}
	} else if b64CC != "" {
		rpcRequest.Headers = map[string]interface{}{"X-Amz-Client-Context": b64CC}
	} else if xray != "" {
		rpcRequest.Headers = map[string]interface{}{"X-Amzn-Trace-Id": xray}
	}

	invoker.producer.Send(rpcRequest)

	responseChan := make(chan []byte)

	invoker.Lock()
	// Don't use defer invoker.Unlock() here as we want critical section small.
	// Store responseChan in a map indexed (in a concurrent-safe way) by cid.
	invoker.pendingRequests[cid] = responseChan
	invoker.Unlock()

	var response []byte

	select {
	case invocationResponse := <-responseChan:
		response = invocationResponse
	case <-ctx.Done():
		// If the invocationResponse is cancelled it means that the
		// AMQP rpcResponseHandler hasn't been called, so we need to
		// remove the pendingRequests entry to prevent a resource leak.
		// In general this should be rare as the RPC requests are
		// handled by Lambda Runtime API Daemon instances which have
		// an AWS_LAMBDA_FUNCTION_TIMEOUT, so should send a valid
		// Task timed out errorMessage response. This path is really
		// a fail safe for cases where a request was successfully
		// routed to a Lambda but the Lambda subsequently fails
		// without sending any response whatsoever.
		invoker.Lock()
		delete(invoker.pendingRequests, cid)
		invoker.Unlock()

		// Check the error and only write timeout response if the
		// cancellation is actually due to a timeout.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			// The timeoutMessage and timeoutResponse utility functions are
			// actually defined in rapiinvoker.go
			message := timeoutMessage(cid, float64(invoker.timeout))
			log.Info(message)
			response = timeoutResponse(message)
		}
	}

	return response
}

func (invoker *RPCInvoker) Close() {
	invoker.close()
}
