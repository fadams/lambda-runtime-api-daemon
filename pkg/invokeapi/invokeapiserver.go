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
	log "github.com/sirupsen/logrus" // Structured logging
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	// Chose chi over github.com/gorilla/mux as it seems the more active project
	// https://pkg.go.dev/github.com/go-chi/chi/v5https://www.bbc.co.uk/news/business-64708230
	"github.com/go-chi/chi/v5"
)

type InvokeAPIServer struct {
	close func()
}

func NewInvokeAPIServer(uri string, invoker Invoker) *InvokeAPIServer {
	srv := &InvokeAPIServer{
		close: func() {}, // NOOP default implementation
	}

	// Handler for the AWS Lambda Invoke API invocations method
	// https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html
	// This handler retrieves the function name from the invoke URI and gets
	// the correlationID and Base64 encoded Client Context from the HTTP
	// headers, then reads the body into a byte slice. Its primary role
	// though is to delegate to the invoke() method, which is intended to be
	// agnostic of the InvokeAPI Server implementation, so we use the same
	// invoke() for HTTP or AMQP-RPC invocations.
	invocations := func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "function") // Get function name from Invoke URI

		// Use deliberately zero timestamp here (will be set by invoker)
		var t time.Time

		headers := r.Header
		// Use Invocation ID as the correlationID if set. If Invocation ID is
		// not set the invoker.invoke call will generate one.
		correlationID := headers.Get("Amz-Sdk-Invocation-Id")
		// Base64 encoded Client Context as sent from Client, will often be empty.
		b64CC := headers.Get("X-Amz-Client-Context")

		// Get the AWS X-Ray Tracing Header from the invocation (if present)
		// https://docs.aws.amazon.com/xray/latest/devguide/xray-concepts.html#xray-concepts-tracingheader
		// TODO The only tracing that is _reliably_ aupported in AWS proper is
		// X-Ray, and it's not yet totally clear how one might use Jaeger or
		// even Open Telemetry. This needs investigation. It's possible that
		// AWS support additional Open Telemetry headers and I *believe* that
		// it is possible to propagate Open Telemetry spans via X-Ray as per:
		// https://github.com/open-telemetry/opentelemetry-python-contrib/tree/main/propagator/opentelemetry-propagator-aws-xray
		// https://github.com/open-telemetry/opentelemetry-js-contrib/tree/main/propagators/opentelemetry-propagator-aws-xray
		// https://github.com/open-telemetry/opentelemetry-go-contrib/tree/main/propagators/aws
		// https://betterprogramming.pub/trace-context-propagation-with-opentelemetry-b8816f2f065e
		// For now just ensure the X-Amz-Client-Context header is passed through.
		xray := headers.Get("X-Amzn-Trace-Id")

		if r.Body != nil {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				log.Errorf("InvokeAPI failed to read invoke body: %s", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Write(invoker.invoke(r.Context(), t, name, correlationID, b64CC, xray, body))
		}
	}

	// Handler for the OpenFaaS /_/health and /_/ready API methods.
	// For now just return http.StatusOK and write "OK" to keep OpenFaaS
	// Gateway happy so we can "pretend" to be an OpenFaaS "Watchdog" to
	// provide basic initial OpenFaaS support. TODO we may want to provide
	// a more complete health/readiness check.
	openFaaSHealth := func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	}

	// Create AWS Lambda Invoke API Server and API routes.
	invokeRouter := chi.NewRouter()

	// Lambda Invoke API https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html route.
	invokeRouter.Post("/2015-03-31/functions/{function}/invocations", invocations)

	// Optional OpenFaaS routes.
	if strings.ToUpper(os.Getenv("ENABLE_OPENFAAS")) == "TRUE" {
		log.Info("InvokeAPI enabling OpenFaaS routes")
		invokeRouter.Post("/", invocations)
		invokeRouter.Get("/_/health", openFaaSHealth)
		invokeRouter.Get("/_/ready", openFaaSHealth)
	}

	invokeServer := &http.Server{
		Addr:    uri, // Default is 0.0.0.0:8080
		Handler: invokeRouter,
	}

	// Concrete close implementation cleanly calls http.Server.Shutdown()
	srv.close = func() {
		invoker.Close() // Cleanly close the invoker implementation
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := invokeServer.Shutdown(ctx); err != nil {
			// Error from closing listeners, or context timeout:
			log.Warnf("InvokeAPI Shutdown: %v", err)
		}
	}

	go func() {
		log.Infof("InvokeAPI listening on %s", uri)
		if err := invokeServer.ListenAndServe(); err != nil {
			log.Infof("InvokeAPI ListenAndServe %v", err)
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

	return srv
}

// Cleanly close the InvokeAPIServer. Delegates to a concrete implementation
// that is assigned by the implementation specific factory method.
func (srv *InvokeAPIServer) Close() {
	srv.close()
}
