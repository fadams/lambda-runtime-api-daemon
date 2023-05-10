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
	log "github.com/sirupsen/logrus" // Structured logging
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	// Chose chi over github.com/gorilla/mux as it seems the more active project
	// https://pkg.go.dev/github.com/go-chi/chi/v5
	"github.com/go-chi/chi/v5"

	"lambda-runtime-api-daemon/pkg/config/rapid"
	"lambda-runtime-api-daemon/pkg/process"
)

func NewRuntimeAPIServer(cfg *rapid.Config, pm *process.ProcessManager) *RuntimeAPI {
	rapi := &RuntimeAPI{
		pm:              pm,
		extensions:      NewExtensionsAPI(cfg),
		Invocations:     make(chan Request),
		InitError:       make(chan Response, 1), // Needs to buffer one item.
		pendingRequests: make(map[string]chan Response),
		shutdown:        make(chan struct{}, 1), // Needs to buffer one item.
		initialisers:    make([]sync.Once, cfg.MaxConcurrency),
		idleTimeout:     cfg.IdleTimeout,
	}
	pm.SetRegisterHandler(rapi.RegisterLambdaRuntime)
	pm.SetUnregisterHandler(rapi.UnregisterLambdaRuntime)

	// Create AWS Lambda Runtime API Server and API routes.
	rapiRouter := chi.NewRouter()

	// Runtime API routes.
	rapiRouter.HandleFunc("/2018-06-01/runtime/invocation/next", rapi.next)
	rapiRouter.HandleFunc("/2018-06-01/runtime/invocation/{id}/response", rapi.response)
	rapiRouter.HandleFunc("/2018-06-01/runtime/init/error", rapi.initerror)
	// Use the rapi.response handler to handle invocation error too.
	rapiRouter.HandleFunc("/2018-06-01/runtime/invocation/{id}/error", rapi.response)

	// Extensions API routes.
	rapiRouter.HandleFunc("/2020-01-01/extension/register", rapi.extensions.register)
	rapiRouter.HandleFunc("/2020-01-01/extension/event/next", rapi.extensions.next)
	//rapiRouter.HandleFunc("/2020-01-01/extension/init/error", rapi.extensions.initerror)
	//rapiRouter.HandleFunc("/2020-01-01/extension/exit/error", rapi.extensions.exiterror)
	server := &http.Server{
		Addr:    cfg.RuntimeAPIServerURI, // Default is 127.0.0.1:9001
		Handler: rapiRouter,
	}
	if rapi.extensions.EventsEnabled() && cfg.MaxConcurrency > 1 {
		// If Extensions are enabled set the ConnState handler to trigger
		// a method that maps the remote address, e.g. the address that
		// the Extensions use to call the Extensions API, to Extension.
		// "handles". We can use the Extension's pid and pgid to associate
		// the Extension instances with the appropriate Runtime instances.
		server.ConnState = rapi.extensions.MapRemoteAddrToExtensions
	}

	// Concrete close implementation cleanly calls http.Server.Shutdown()
	rapi.close = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			// Error from closing listeners, or context timeout:
			log.Warnf("RuntimeAPI Shutdown: %v", err)
		}
	}

	go func() {
		log.Infof("RuntimeAPI listening on %s", cfg.RuntimeAPIServerURI)
		if err := server.ListenAndServe(); err != nil {
			log.Infof("RuntimeAPI ListenAndServe %v", err)
			if err == http.ErrServerClosed {
				// ErrServerClosed is caused by Shutdown so wait for other
				// goroutines to cleanly exit.
				runtime.Goexit()
			} else {
				// For other errors terminate immediately
				os.Exit(1)
			}
		}
	}()

	return rapi
}
