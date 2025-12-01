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
	log "github.com/sirupsen/logrus" // Structured logging

	"lambda-runtime-api-daemon/pkg/config/util"
)

// lambda-server version
var version = "unknown" // will be overwritten by ldflags

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
	invokeAPIHost := util.Getenv("INVOKE_API_HOST", defaultInvokeAPIHost)
	invokeAPIPort := util.Getenv("PORT", defaultInvokeAPIPort)

	// The connection URI to an AMQP message broker can either be set with a
	// full amqp:// URI of the form:
	// amqp://username:password@host:port/virtual_host?key=value&key=value
	// or a shorter form URI amqp://host:port with username and password set
	// separately via AMQP_USERNAME and AMQP_PASSWORD environment variables.
	amqpURI := util.Getenv("AMQP_URI", defaultRPCServerURI)
	amqpUsername := util.Getenv("AMQP_USERNAME", "")
	amqpPassword := util.Getenv("AMQP_PASSWORD", "")

	rpcServerURI := util.InjectAMQPCredentials(amqpURI, amqpUsername, amqpPassword)

	timeout := util.GetenvInt(
		"AWS_LAMBDA_FUNCTION_TIMEOUT",
		AWS_LAMBDA_FUNCTION_TIMEOUT_DEFAULT,
	)

	log.Infof("Version: %s", version)

	config := &Config{
		InvokeAPIServerURI: invokeAPIHost + ":" + invokeAPIPort,
		RPCServerURI:       rpcServerURI,
		Timeout:            timeout,
	}

	return config
}
