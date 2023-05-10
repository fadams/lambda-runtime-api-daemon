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

# Demo of Lambda extensions using custom container image & custom Lambda runtime
# It is based on the AWS sample:
# https://github.com/aws-samples/aws-lambda-extensions/tree/main/custom-runtime-extension-demo
# Which is the demo presented in the AWS blog post:
# https://aws.amazon.com/blogs/compute/building-extensions-for-aws-lambda-in-preview/
# The original example used Lambda zip packaging so this repo illustrates an
# update packaged as a Lambda container image. The function, runtime and
# extension code is identical to the original except for a small tweak to the
# bootstrap (runtime) script as the original had hard coded LAMBDA_TASK_ROOT
# whereas it's prefereable to be able to set this from an environment
# variable, so that line is commented out here.

FROM ubuntu:20.04

ENV LAMBDA_TASK_ROOT=/usr/local/lib
# See also https://docs.aws.amazon.com/lambda/latest/dg/configuration-envvars.html#configuration-envvars-runtime

# (Optional) Add Lambda Runtime Interface Emulator
#ADD https://github.com/aws/aws-lambda-runtime-interface-emulator/releases/latest/download/aws-lambda-rie /usr/local/bin/aws-lambda-rie

# Use the custom runtime script
COPY bootstrap /

# Use a script in the ENTRYPOINT for simpler local runs
COPY lambda-entrypoint.sh /

# Copy function code
COPY function.sh ${LAMBDA_TASK_ROOT}

# Copy extensions
COPY extensions /opt/extensions

RUN apt-get update && DEBIAN_FRONTEND=noninteractive \
    apt-get install -y --no-install-recommends \
    curl && \
    chmod -R 755 /usr/local/bin && \
    chmod 755 /bootstrap /lambda-entrypoint.sh && \
    apt-get clean && \
    apt-get autoremove -y && \
    # Clean up pip/apt/dpkg cache & logs
    rm -rf /root/.cache && \
    # Internet resources often show the following WRT Dockerfile pruning:
    # rm -rf /var/lib/{apt,dpkg,cache,log}
    # but that doesn't *actually* work in Dockerfiles, as it relies on bash
    # brace expansion, whereas Dockerfile RUN uses /bin/sh by default.
    # In addition removing /var/lib/dpkg actually breaks apt in child images.
    # The following does what that rm is _supposed_ to do:
    cd /var/lib && rm -rf apt cache log

# Set working directory to function root directory
WORKDIR ${LAMBDA_TASK_ROOT}

ENTRYPOINT ["/lambda-entrypoint.sh"] 

# Set the CMD to handler
CMD ["function.handler"]

#-------------------------------------------------------------------------------
# docker build -t custom-runtime-extension .

# Run using AWS Lambda RIE (if installed)
# docker run --rm -u 1000:1000 -p 8080:8080 custom-runtime-extension

# Use this to bind-mount lambda-runtime-api-daemon
# docker run --rm -u 1000:1000 -p 8080:8080 -v ${PWD}/lambda-runtime-api-daemon:/usr/local/bin/aws-lambda-rie -e AWS_LAMBDA_FUNCTION_NAME=custom-runtime-extension -e AMQP_URI="amqp://$(hostname -I | awk '{print $1}'):5672?connection_attempts=20&retry_delay=10&heartbeat=0" custom-runtime-extension


# Different ways to invoke:
# Note that the AWS Lambda RIE only supports the FunctionName function in the
# Invoke URI, but the Lambda Runtime API Daemon supports URIs with
# FunctionName set to AWS_LAMBDA_FUNCTION_NAME e.g. custom-runtime-extension

# curl -XPOST "http://localhost:8080/2015-03-31/functions/function/invocations" -d '{"key": "value"}'

# Works with Lambda Runtime API Daemon but not with AWS Lambda RIE
# curl -XPOST "http://localhost:8080/2015-03-31/functions/custom-runtime-extension/invocations" -d '{"key": "value"}'

# aws lambda invoke --endpoint-url http://localhost:8080 --function-name function --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null

# Works with Lambda Runtime API Daemon but not with AWS Lambda RIE as the
# function-name needs to be function for the RIE.
# aws lambda invoke --endpoint-url http://localhost:8080 --function-name custom-runtime-extension --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null

