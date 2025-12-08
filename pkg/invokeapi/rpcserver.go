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
	"lambda-runtime-api-daemon/pkg/messaging"
	"log/slog"
	"os"
	"runtime"
)

func NewRPCServer(uri string, name string, concurrency int, invoker Invoker) *InvokeAPIServer {
	srv := &InvokeAPIServer{
		close: func() {}, // NOOP default implementation
	}

	if uri == "" {
		slog.Warn("No Broker Connection URI Set, RPCServer Has Been Disabled")
		return srv
	}

	go func() {
		defer os.Exit(1) // Exiting this goroutine is fatal, so force os.Exit

		ctx, cancel := context.WithCancel(context.Background())

		// Concrete close implementation calls cancel() on the base context.
		srv.close = func() {
			invoker.Close() // Cleanly close the invoker implementation
			cancel()
			runtime.Goexit() // Let cancel() cleanly stop the RPCServer and run defers.
		}

		exitOnError := func(err error, msg string) {
			if err != nil {
				slog.Info(
					"Exiting:",
					slog.String("message", msg), slog.Any("error", err),
				)
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
		// separate connections for consuming RPC requests and sending the
		// corresponding RPC responses because it mitigates potential TCP
		// pushback on publishing affecting consumer throughput. Moreover, it
		// mitigates the lock contention on the Connection that would exist
		// between Message acks and Message sends when the RPC handlers are
		// launched as goroutines (which they need to be to allow concurrent
		// RPC invocations). TL;DR separate connections increases throughput.
		// amqp://guest:guest@localhost:5672?connection_attempts=20&retry_delay=10&heartbeat=0
		cConnection, err := messaging.NewConnection(
			uri,
			messaging.Context(ctx),
			messaging.ConnectionName("RPC Consumer Connection"),
		)
		exitOnError(err, "RPC Consumer Connection failed to connect to AMQP broker")
		defer cConnection.Close()

		pConnection, err := messaging.NewConnection(
			uri,
			messaging.Context(ctx),
			messaging.ConnectionName("RPC Producer Connection"),
		)
		exitOnError(err, "RPC Producer Connection failed to connect to AMQP broker")
		defer pConnection.Close()

		// It's important to *not* use messaging.AutoAck on the consumer
		// Session. It can potentially improve throughput as acknowledgement
		// is done by the broker, so eliminates the network and syscall cost
		// of the Ack. The down side, however, is that messages won't be
		// redelivered on failure, but a more significant down side is that
		// rpcHandlers are launched as goroutines and if AutoAck is set
		// the mumber of handler goroutines could grow very large if the
		// message delivery rate is higher than the ability to service
		// invocation requests. By using explicit acknowledgement and a
		// sensible message prefetch/capacity/QoS size then the number of
		// in-flight rpcHandler goroutines is then bounded to that number.
		// TODO Rather than AutoAck on one end of the spectrum and Acking
		// each message on the other it may be possible to "batch" Ack.
		cSession, err := cConnection.Session(messaging.SessionName("RPC consumer Session"))
		exitOnError(err, "Failed to open RPC consumer Session")
		defer cSession.Close()

		pSession, err := pConnection.Session(messaging.SessionName("RPC producer Session"))
		exitOnError(err, "Failed to open RPC producer Session")
		defer pSession.Close()

		// Uses AWS_LAMBDA_FUNCTION_NAME or handler name from config.
		consumer, err := cSession.Consumer(
			//name + `; {"node": {"auto-delete": true}}`, messaging.Pool(2),
			name + `; {"node": {"auto-delete": true}}`,
		)
		exitOnError(err, "Failed to create RPC Consumer") // Should be unlikely

		// Set Consumer message prefetch/capacity/QoS
		// 5 * concurrency is somewhat arbitrary but provides some additional
		// "buffering" and improved throughput compared with simply setting
		// the prefetch to the configured concurrency value.
		consumer.SetCapacity(5 * concurrency)

		// Create Producer for AMQP RPC replies. The "" address means use the
		// default direct exchange and the Message Subject will be used as the
		// Routing Key, which in this case will be the ReplyTo from the request.
		reply, err := pSession.Producer("")
		exitOnError(err, "Failed to create RPC Producer") // Should be unlikely

		rpcHandler := func(message messaging.Message) {
			//log.Printf("Received a message: %s", message.Body)
			//log.Println(message)

			// Get the X-Amz-Client-Context from the AMQP headers (if present).
			b64CC := ""
			if val, ok := message.Headers["X-Amz-Client-Context"]; ok { // Do lookup
				// AMQP headers are interface types, so type assert to string.
				b64CC = val.(string)
			}

			// Get the X-Amzn-Trace-Id from the AMQP headers (if present).
			// See comments in invokeapiserver.go about usage of the
			// AWS X-Ray Tracing Header and approaches for propagating
			// Open Telemetry spans via X-Ray.
			xray := ""
			if val, ok := message.Headers["X-Amzn-Trace-Id"]; ok { // Do lookup
				// AMQP headers are interface types, so type assert to string.
				xray = val.(string)
			}

			message.Body = invoker.invoke(
				ctx,
				message.Timestamp, // May be zero or may be set by Lambda Server
				name,
				message.CorrelationID,
				b64CC,
				xray,
				message.Body,
			)
			message.Subject = message.ReplyTo
			message.ReplyTo = ""
			reply.Send(message)

			message.Acknowledge(messaging.Multiple(false))
		}

		// Get the Message Consume and Connection CloseNotify channels outside
		// the loop as Consume() employs a lock and CloseNotify() creates the
		// chan, registers it as a listener then returns the chan, which we
		// don't want to do each iteration.
		msgs := consumer.Consume()
		pCloseNotify := cConnection.CloseNotify()
		cCloseNotify := pConnection.CloseNotify()
	loop:
		for {
			select {
			// Handle Messages from the Consumer channel
			case m := <-msgs:
				// Handler launched as goroutine to enable concurrent requests.
				go rpcHandler(m)
			// Listen on the CloseNotify channels and exit on error if broker
			// connection fails and the maximum Connection Attempts is exceeded.
			case err := <-pCloseNotify:
				exitOnError(err, "RPC Consumer Connection to AMQP broker was "+
					"closed and maximum Connection Attempts exceeded")
			case err := <-cCloseNotify:
				exitOnError(err, "RPC Producer Connection to AMQP broker was "+
					"closed and maximum Connection Attempts exceeded")
			case <-ctx.Done(): // Exit main loop when cancel function is called.
				break loop
			}
		}

		slog.Info("RPCServer Stopped")
	}()

	return srv
}
