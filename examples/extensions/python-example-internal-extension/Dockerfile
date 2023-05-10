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

# Simple Python Lambda with a Python External extension using a custom Lambda
# container image. This example is based on the AWS demo:
# https://github.com/aws-samples/aws-lambda-extensions/tree/main/python-example-extension
# This example uses default Ubuntu python3 and pip3 packages and tidies up
# after installing, removing pip, pip and apt cache etc.

FROM ubuntu:20.04

ENV LAMBDA_TASK_ROOT=/usr/local/lib
# See also https://docs.aws.amazon.com/lambda/latest/dg/configuration-envvars.html#configuration-envvars-runtime

# (Optional) Add Lambda Runtime Interface Emulator
#ADD https://github.com/aws/aws-lambda-runtime-interface-emulator/releases/latest/download/aws-lambda-rie /usr/local/bin/aws-lambda-rie

# Use a script in the ENTRYPOINT for simpler local runs
COPY lambda-entrypoint.sh /

# Copy function code
COPY lambda_function.py ${LAMBDA_TASK_ROOT}/
COPY requirements.txt ${LAMBDA_TASK_ROOT}/

RUN apt-get update && DEBIAN_FRONTEND=noninteractive \
    apt-get install -y --no-install-recommends \
    python3 python3-pip && \
    # Install Lambda Runtime Interface Client for Python
    pip3 install awslambdaric && \
    # Install Extension dependencies
    pip3 install -r ${LAMBDA_TASK_ROOT}/requirements.txt && \
    chmod -R 755 /usr/local/bin && \
    chmod 755 /lambda-entrypoint.sh && \
    apt-get clean && \
    apt-get purge -y python3-pip && \
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
CMD ["lambda_function.lambda_handler"]

#-------------------------------------------------------------------------------
# docker build -t python-example-internal-extension .

# Run using AWS Lambda RIE (if installed)
# docker run --rm -u 1000:1000 -p 8080:8080 python-example-internal-extension

# Use this to bind-mount lambda-runtime-api-daemon
# docker run --rm -u 1000:1000 -p 8080:8080 -v ${PWD}/lambda-runtime-api-daemon:/usr/local/bin/aws-lambda-rie -e AWS_LAMBDA_FUNCTION_NAME=python-example-internal-extension -e AMQP_URI="amqp://$(hostname -I | awk '{print $1}'):5672?connection_attempts=20&retry_delay=10&heartbeat=0" python-example-internal-extension


# Different ways to invoke:
# Note that the AWS Lambda RIE only supports the FunctionName function in the
# Invoke URI, but the Lambda Runtime API Daemon supports URIs with
# FunctionName set to AWS_LAMBDA_FUNCTION_NAME e.g. python-example-internal-extension

# curl -XPOST "http://localhost:8080/2015-03-31/functions/function/invocations" -d '{"key": "value"}'

# Works with Lambda Runtime API Daemon but not with AWS Lambda RIE
# curl -XPOST "http://localhost:8080/2015-03-31/functions/python-example-internal-extension/invocations" -d '{"key": "value"}'

# aws lambda invoke --endpoint-url http://localhost:8080 --function-name function --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null

# Works with Lambda Runtime API Daemon but not with AWS Lambda RIE as the
# function-name needs to be function for the RIE.
# aws lambda invoke --endpoint-url http://localhost:8080 --function-name python-example-internal-extension --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null

