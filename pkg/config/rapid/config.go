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

package rapid

import (
	log "github.com/sirupsen/logrus" // Structured logging
	"os"
	"strings"

	"lambda-runtime-api-daemon/pkg/config/env"
)

const (
	//defaultPrintReportsValue = "TRUE"
	defaultPrintReportsValue = "FALSE"

	// https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html
	defaultInvokeAPIHost = "0.0.0.0"
	defaultInvokeAPIPort = "8080"

	// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html
	defaultRuntimeAPIHost = "127.0.0.1"
	defaultRuntimeAPIPort = "9001"

	// If no AMQP-RPC broker connection URI is explicitly supplied we default
	// to "", which will "disable" any connection attempts to the broker.
	// This is useful as it allows the Lambda Runtime API Daemon to be used
	// as a purely REST service and can be used standalone in place of the
	// AWS Lambda Runtime Interface Emulator.
	defaultRPCServerURI = ""

	//defaultMaxConcurrency int = 10
	defaultMaxConcurrency int = 1

	// https://docs.aws.amazon.com/lambda/latest/dg/configuration-function-common.html#configuration-timeout-console
	AWS_LAMBDA_FUNCTION_TIMEOUT_DEFAULT int = 3

	AWS_LAMBDA_FUNCTION_MEMORY_SIZE_DEFAULT int = 3008

	// https://docs.aws.amazon.com/lambda/latest/dg/configuration-versions.html
	AWS_LAMBDA_FUNCTION_VERSION_DEFAULT string = "$LATEST"

	AWS_LAMBDA_FUNCTION_IDLETIMEOUT_DEFAULT int = 1800 // 1800 seconds = 30 mins
)

type Config struct {
	InvokeAPIServerURI  string
	RuntimeAPIServerURI string
	RPCServerURI        string
	FunctionName        string
	Version             string
	Handler             string
	Cwd                 string
	Cmd                 []string
	Env                 []string
	Timeout             int
	IdleTimeout         int
	Memory              int
	MaxConcurrency      int
	Report              bool
}

// Returns a populated Config instance for use by the rest of the application.
// Most configurable fields are configured via environment variables and the
// Config struct and this factory simply centralises this.
func GetConfig() *Config {
	invokeAPIHost := env.Getenv("INVOKE_API_HOST", defaultInvokeAPIHost)
	invokeAPIPort := env.Getenv("PORT", defaultInvokeAPIPort)
	runtimeAPIHost := env.Getenv("RUNTIME_API_HOST", defaultRuntimeAPIHost)
	runtimeAPIPort := env.Getenv("RUNTIME_API_PORT", defaultRuntimeAPIPort)
	rpcServerURI := env.Getenv("AMQP_URI", defaultRPCServerURI)
	name := env.Getenv("AWS_LAMBDA_FUNCTION_NAME", "")
	version := env.Getenv(
		"AWS_LAMBDA_FUNCTION_VERSION",
		AWS_LAMBDA_FUNCTION_VERSION_DEFAULT,
	)
	timeout := env.GetenvInt(
		"AWS_LAMBDA_FUNCTION_TIMEOUT",
		AWS_LAMBDA_FUNCTION_TIMEOUT_DEFAULT,
	)
	idleTimeout := env.GetenvInt(
		"AWS_LAMBDA_FUNCTION_IDLETIMEOUT",
		AWS_LAMBDA_FUNCTION_IDLETIMEOUT_DEFAULT,
	)
	memory := env.GetenvInt(
		"AWS_LAMBDA_FUNCTION_MEMORY_SIZE",
		AWS_LAMBDA_FUNCTION_MEMORY_SIZE_DEFAULT,
	)
	maxConcurrency := env.GetenvInt("MAX_CONCURRENCY", defaultMaxConcurrency)
	report := strings.ToUpper(
		env.Getenv("PRINT_REPORTS", defaultPrintReportsValue)) == "TRUE"

	config := &Config{
		InvokeAPIServerURI:  invokeAPIHost + ":" + invokeAPIPort,
		RuntimeAPIServerURI: runtimeAPIHost + ":" + runtimeAPIPort,
		RPCServerURI:        rpcServerURI,
		FunctionName:        name,
		Version:             version,
		Timeout:             timeout,
		IdleTimeout:         idleTimeout,
		Memory:              memory,
		MaxConcurrency:      maxConcurrency,
		Report:              report,
	}

	// Normal usage of the Lambda Runtime API Daemon is something like:
	// lambda-rapid python3 -m awslambdaric echo.handler
	// The following block gets the current working directory and the args
	// we need to actually launch the Lambda. If no args are supplied
	// we fall back to some standard paths for a bootstrap handler
	// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-custom.html
	config.Cwd = "/var/task" // default value
	args := os.Args
	if len(args) > 1 { // Use args to invoke Runtime Interface Client
		config.Cmd = args[1:]
		if cwd, err := os.Getwd(); err == nil {
			config.Cwd = cwd
		}

		if len(args) > 2 { // Assume last arg is the handler
			config.Handler = args[len(args)-1]
		} else { // Otherwise fall back to _HANDLER environment variable if set
			config.Handler = env.Getenv("_HANDLER", "")
		}
	} else { // If any of the candidate bootstrap files exist set Cmd to that
		candidates := []string{"/var/task/bootstrap", "/opt/bootstrap",
			"/var/runtime/bootstrap"}
		for _, candidate := range candidates {
			file, err := os.Stat(candidate)
			if !os.IsNotExist(err) && !file.IsDir() {
				config.Cmd = []string{candidate}
				break
			}
		}
		// If none of the candidate bootstrap files exist set to default
		if len(config.Cmd) == 0 {
			config.Cmd = []string{"/var/task/bootstrap"}
		}
	}

	// Infer AWS_LAMBDA_FUNCTION_NAME from handler if not explicitly set.
	if config.FunctionName == "" {
		if config.Handler == "" {
			log.Warn("Neither AWS_LAMBDA_FUNCTION_NAME nor a handler are " +
				"set, unable to infer FunctionName")
		} else {
			config.FunctionName = strings.Split(config.Handler, ".")[0]
			log.Warnf("AWS_LAMBDA_FUNCTION_NAME is not set, setting "+
				"FunctionName to %s inferred from handler %s",
				config.FunctionName, config.Handler)
		}
	}
	log.Infof("FunctionName: %s", config.FunctionName)
	log.Infof("AWS_LAMBDA_FUNCTION_TIMEOUT: %d", config.Timeout)
	log.Infof("MAX_CONCURRENCY: %d", config.MaxConcurrency)

	// These are forwarded environment variables, set as the Lambda is exec'ed.

	// These settings are required by the Runtime Interface Client/bootstrap
	// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-custom.html
	config.Env = []string{
		"AWS_LAMBDA_RUNTIME_API=" + config.RuntimeAPIServerURI,
		"AWS_LAMBDA_FUNCTION_NAME=" + config.FunctionName,
		"_HANDLER=" + config.Handler,
		"PWD=" + config.Cwd,
	}
	// Enumerate system (container) environment variables and apply a
	// deny list, then add the allowed env vars to config.Env.
	deny := map[string]bool{"PWD": true, "TERM": true, "SHLVL": true, "_": true,
		"AWS_LAMBDA_FUNCTION_NAME": true, "AMQP_URI": true}
	for _, element := range os.Environ() {
		// Split only by first = to cater for env values that may contain an =
		kv := strings.SplitN(element, "=", 2)
		if !deny[kv[0]] {
			config.Env = append(config.Env, element)
		}
	}

	return config
}
