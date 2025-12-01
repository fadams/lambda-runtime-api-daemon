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

// Provides an implementation of an AWS Lambda Server.
//
// The Lambda Server implements the AWS Lambda Invoke API:
// https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html
// so that client applications can, either directly via the Invoke REST API or
// via AWS SDKs like boto3, invoke Lambda Functions by calling the Invoke API,
// which will in turn pass the invocation over AMQP-RPC to be routed to
// the Runtime API Daemon servicing the requested Function.
//
// Whilst each Lambda Runtime API Daemon instance services a specific
// Function type, the Lambda Server acts like the AWS Lambda Service,
// accepting any FunctionName from the Invoke API request URI:
// /2015-03-31/functions/FunctionName/invocations?Qualifier=Qualifier
// and routing that to the required Runtime API Daemon via AMQP.

package main

import (
	"lambda-runtime-api-daemon/pkg/config/server"
	"lambda-runtime-api-daemon/pkg/config/util"
	"lambda-runtime-api-daemon/pkg/invokeapi"
	"lambda-runtime-api-daemon/pkg/logging"
	"lambda-runtime-api-daemon/pkg/process"
)

func main() {
	logging.SetLogLevel(util.Getenv("LOG_LEVEL", "INFO"))
	cfg := server.GetConfig()

	// ProcessManager manages signals.
	pm := process.NewProcessManager()

	// Run InvokeAPIServer in a goroutine and cleanly stop on exit.
	iapi := invokeapi.NewInvokeAPIServer(
		cfg.InvokeAPIServerURI,
		invokeapi.NewRPCInvoker(cfg),
	)
	defer iapi.Close()

	// Handle signals, blocking until exit
	pm.HandleSignals()
}
