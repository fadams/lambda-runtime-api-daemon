#
# Licensed to the Apache Software Foundation (ASF) under one
# or more contributor license agreements.  See the NOTICE file
# distributed with this work for additional information
# regarding copyright ownership.  The ASF licenses this file
# to you under the Apache License, Version 2.0 (the
# "License"); you may not use this file except in compliance
# with the License.  You may obtain a copy of the License at
# 
#   http://www.apache.org/licenses/LICENSE-2.0
# 
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.
#

# Simple go Lambda built as a static binary with a static binary ENTRYPOINT
# to allow running via rapid or rie or from AWS Lambda based on whether
# the AWS_LAMBDA_RUNTIME_API environment variable is set. Using those two
# static binaries in a scratch image means that the Lambda Container Image
# is only around 4.8MB in size.

FROM golang:1.18 as builder
WORKDIR /go/src/echo-lambda
COPY . .
RUN apt-get update && DEBIAN_FRONTEND=noninteractive \
    apt-get install -y --no-install-recommends \
    musl-tools && \
    make dep && make entrypoint && make echo

# Copy artifacts to a scratch image
FROM scratch
#FROM ubuntu:20.04 # For debugging in case scratch image blows up
COPY --from=builder /go/src/echo-lambda/echo /echo
COPY --from=builder /go/src/echo-lambda/entrypoint /entrypoint

# Set ENTRYPOINT to run with AWS Lambda if AWS_LAMBDA_RUNTIME_API is set
# or rapid (or rie) if AWS_LAMBDA_RUNTIME_API is not set.
ENTRYPOINT ["/entrypoint"]

# Set the CMD to handler
CMD ["/echo"]

#-------------------------------------------------------------------------------
# docker build -t echo-lambda-go .

# Run using AWS Lambda RIE (if installed)
# docker run --rm -u 1000:1000 -p 8080:8080 echo-lambda-go

# Use this to bind-mount lambda-runtime-api-daemon
# docker run --rm -u 1000:1000 -p 8080:8080 -v ${PWD}/lambda-runtime-api-daemon:/usr/local/bin/aws-lambda-rie -e AWS_LAMBDA_FUNCTION_NAME=echo-lambda -e AMQP_URI="amqp://$(hostname -I | awk '{print $1}'):5672?connection_attempts=20&retry_delay=10&heartbeat=0" echo-lambda-go


# Different ways to invoke:
# Note that the AWS Lambda RIE only supports the FunctionName function in the
# Invoke URI, but the Lambda Runtime API Daemon supports URIs with
# FunctionName set to AWS_LAMBDA_FUNCTION_NAME e.g. echo-lambda

# curl -XPOST "http://localhost:8080/2015-03-31/functions/function/invocations" -d '{"key": "value"}'

# Works with Lambda Runtime API Daemon but not with AWS Lambda RIE
# curl -XPOST "http://localhost:8080/2015-03-31/functions/echo-lambda/invocations" -d '{"key": "value"}'

# Works with Lambda Runtime API Daemon but not with AWS Lambda RIE. Note the
# quoted JSON string. If just "hello" is used the Lambda will fail to unmarshall
# because it's not valid JSON.
# curl -XPOST "http://localhost:8080/2015-03-31/functions/echo-lambda/invocations" -d '"hello"'

# aws lambda invoke --endpoint-url http://localhost:8080 --function-name function --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null

# Works with Lambda Runtime API Daemon but not with AWS Lambda RIE as the
# function-name needs to be function for the RIE.
# aws lambda invoke --endpoint-url http://localhost:8080 --function-name echo-lambda --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null

