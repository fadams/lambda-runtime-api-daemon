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

package server

import (
	"lambda-runtime-api-daemon/pkg/config/env"
	"net/url"
)

const (
	// https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html
	defaultInvokeAPIHost = "0.0.0.0"
	defaultInvokeAPIPort = "8080"

	// Note that the default localhost address won't behave in the way that
	// may be expected if the Lambda Server is deployed in a container, as
	// localhost will refer to the *container's* localhost, which is unlikely
	// to be where the AMQP broker is bound to. In practice therefore for
	// container deployments the AMQP_URI environment variable most likely
	// will need to be set with the required broker URI.
	defaultRPCServerURI = "amqp://guest:guest@localhost:5672?connection_attempts=20&retry_delay=10&heartbeat=0"

	// Note that for the Lambda Server we can't know the timeouts set
	// for each Lambda as they are decoupled by a messaging fabric.
	// In most cases the Lambdas will send a response message when they
	// time out, but we also have a fail safe timeout here for cases
	// where a Lambda fails between receiving a request and sending a
	// response. This timeout needs to be higher than the highest
	// expected legitimate Lambda timeout. We set the default to be
	// 1800 seconds (30 minutes) as that is twice the AWS Lambda max
	// timeout of 15 minutes and also happens to be the RabbitMQ ack
	// timeout where the broker closes a channel if a message acknowledge
	// hasn't occurred within 30 minutes of the message being delivered.
	// https://docs.aws.amazon.com/lambda/latest/dg/configuration-function-common.html#configuration-timeout-console
	//
	// Invocation requests to non-existent/non-deployed Lambdas are
	// identified quickly as requests are published with Mandatory set
	// true, so if they are unroutable the will be returned by the broker
	// so the purpose of this timeout is to cater for cases where
	// the requests have actually succeeded, but the Lambda fails to
	// respond at all.
	AWS_LAMBDA_FUNCTION_TIMEOUT_DEFAULT int = 1800
)

type Config struct {
	InvokeAPIServerURI string
	RPCServerURI       string
	Timeout            int
}

// Returns a populated Config instance for use by the rest of the application.
// Most configurable fields are configured via environment variables and the
// Config struct and this factory simply centralises this.
func GetConfig() *Config {
	invokeAPIHost := env.Getenv("INVOKE_API_HOST", defaultInvokeAPIHost)
	invokeAPIPort := env.Getenv("PORT", defaultInvokeAPIPort)

	amqpURI := env.Getenv("AMQP_URI", defaultRPCServerURI)
	amqpUsername := env.Getenv("AMQP_USERNAME", "")
	amqpPassword := env.Getenv("AMQP_PASSWORD", "")

	rpcServerURI := injectAMQPCredentials(amqpURI, amqpUsername, amqpPassword)

	timeout := env.GetenvInt(
		"AWS_LAMBDA_FUNCTION_TIMEOUT",
		AWS_LAMBDA_FUNCTION_TIMEOUT_DEFAULT,
	)

	config := &Config{
		InvokeAPIServerURI: invokeAPIHost + ":" + invokeAPIPort,
		RPCServerURI:       rpcServerURI,
		Timeout:            timeout,
	}

	return config
}

func injectAMQPCredentials(rawURI, username, password string) string {
	parsed, err := url.Parse(rawURI)
	if err != nil {
		// If it's not a valid URL, fallback to raw URI
		return rawURI
	}

	// If the URI already includes user info, return as-is
	if parsed.User != nil {
		return rawURI
	}

	// Only inject if both username and password are provided
	if username != "" && password != "" {
		parsed.User = url.UserPassword(username, password)
		return parsed.String()
	}

	// No user info provided
	return rawURI
}
