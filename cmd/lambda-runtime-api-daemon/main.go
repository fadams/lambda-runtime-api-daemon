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

// Provides an implementation of an AWS Lambda "Runtime API Daemon",
// AKA "rapid". This Daemon implements the AWS Lambda Runtime API:
// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html
// so that Lambda Runtimes can connect to it to receive invocations.
//
// The Daemon also implements the AWS Lambda Invoke API:
// https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html
// so that client applications can, either directly via the Invoke REST API or
// via AWS SDKs like boto3, invoke Lambda Functions by calling the Invoke API,
// which will in turn pass the invocation over a channel to rendezvous with the
// next available inbound Runtime API next invocation API call.
//
// The Daemon also enables invocations via an AMQP-RPC pattern
// https://www.rabbitmq.com/tutorials/tutorial-six-go.html
// In this case AWS_LAMBDA_FUNCTION_NAME maps to a queue name that will be
// created and listened to and messages on that queue will correspond to
// Lambda request invocations. As per the AMQP-RPC pattern clients must
// include populated reply_to and correlation_id AMQP properties to allow
// the response message to be sent back to the requestor and associated with
// the original request made by the client.

package main

import (
	"io"
	"lambda-runtime-api-daemon/pkg/config/env"
	"lambda-runtime-api-daemon/pkg/config/rapid"
	"lambda-runtime-api-daemon/pkg/invokeapi"
	"lambda-runtime-api-daemon/pkg/logging"
	"lambda-runtime-api-daemon/pkg/process"
	"lambda-runtime-api-daemon/pkg/runtimeapi"
	"os"
	"path"
)

func main() {
	// KUBERNETES_INIT_CONTAINER being set is a special case that causes the
	// Runtime API Daemon executable to copy itself to /tmp and then exit.
	// The main use case for this is Kubernetes init containers, where we use
	// an init container for the Runtime API Daemon so we can attach a volume
	// and mount the Runtime API Daemon executable into the Lambda container.
	//if strings.ToUpper(env.Getenv("KUBERNETES_INIT_CONTAINER", "")) == "TRUE" {
	if _, ok := os.LookupEnv("KUBERNETES_INIT_CONTAINER"); ok {
		// Get path name for the executable that started the current process.
		if src, err := os.Executable(); err == nil {
			if source, err := os.Open(src); err == nil { // Open the file
				defer source.Close()
				if stats, err := source.Stat(); err == nil { // Get permissions
					dst := "/tmp/" + path.Base(src)
					// Create destination file in /tmp with the same permissions
					// as the source file, then copy source to destination.
					if destination, err := os.Create(dst); err == nil {
						defer destination.Close()
						if destination.Chmod(stats.Mode()) == nil {
							io.Copy(destination, source)
						}
					}
				}
			}
		}
		return
	}

	logging.SetLogLevel(env.Getenv("LOG_LEVEL", "INFO"))
	cfg := rapid.GetConfig()

	// ProcessManager manages spawning and reaping Lambda/Extension processes.
	pm := process.NewProcessManager()

	// Run RuntimeAPIServer in a goroutine and cleanly stop on exit.
	rapi := runtimeapi.NewRuntimeAPIServer(cfg, pm)
	defer rapi.Close()

	// Run InvokeAPIServer in a goroutine and cleanly stop on exit.
	iapi := invokeapi.NewInvokeAPIServer(
		cfg.InvokeAPIServerURI,
		invokeapi.NewRAPIInvoker(cfg, pm, rapi),
	)
	defer iapi.Close()

	// Run AMQP RPCServer in a goroutine and cleanly stop on exit.
	rpc := invokeapi.NewRPCServer(
		cfg.RPCServerURI, cfg.FunctionName, cfg.MaxConcurrency,
		invokeapi.NewRAPIInvoker(cfg, pm, rapi),
	)
	defer rpc.Close()

	// Handle signals, blocking until exit
	pm.HandleSignals()
}
