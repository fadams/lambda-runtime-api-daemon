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
	"time"
)

// Invoker is an interface used by InvokeAPIServer implementations to
// abstract where the invocation is being delegated. This means that
// the Runtime API Daemon's InvokeAPIServer can use the RAPIInvoker to
// pass an invoke request to the RuntimeAPI or the Lambda Server's
// InvokeAPIServer can use the RPCInvoker to pass the invocation over
// AMQP-RPC to be routed to the appropriate Runtime API Daemon instance.
//
// invoke takes as arguments a root Context, an optional timestamp (if
// not supplied invoke should generate one), the Function name, an
// optional correlation ID (if not supplied invoke should generate one),
// an optional base64 client context as specified in the AWS Lambda
// Invoke API documentation:
// https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html#API_Invoke_RequestSyntax
// and finally, invoke takes the invocation body as a byte slice.
//
// invoke will block and return a byte slice representing the response,
// so it is important that any server handlers that might call invoke
// are launched as goroutines to enable concurrency (which will be the
// case for HTTP Server handlers).
//
// Close allows Invoker implementations to be cleanly shut down.
type Invoker interface {
	invoke(
		rctx context.Context,
		timestamp time.Time,
		name string,
		cid string,
		b64CC string,
		body []byte,
	) []byte
	Close()
}
