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

// TODO
package main

import (
	"fmt"
	log "github.com/sirupsen/logrus" // Structured logging

	"lambda-runtime-api-daemon/pkg/config/env"
	"lambda-runtime-api-daemon/pkg/config/server"
	"lambda-runtime-api-daemon/pkg/logging"
	"lambda-runtime-api-daemon/pkg/process"
)

func main() {
	logging.SetLogLevel(env.Getenv("LOG_LEVEL", "INFO"))
	cfg := server.GetConfig()

	log.Info("lambda-server")
	fmt.Println(cfg)

	pm := process.NewProcessManager() // Spawn/reap Lambda/Extension processes
	//rapi := NewRuntimeAPIServer(cfg)  // Run RuntimeAPIServer in goroutine
	//NewInvokeAPIServer(cfg, pm, rapi) // Run InvokeAPIServer in goroutine
	pm.HandleSignals() // Handle signals, blocking until exit
}