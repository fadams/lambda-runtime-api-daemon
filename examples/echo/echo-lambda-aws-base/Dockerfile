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

# Simple Python Lambda using AWS supplied base Lambda container image
# This example is based on the example from the AWS documentation under
# "Working with Python" -> "Deploy container images"
# https://docs.aws.amazon.com/lambda/latest/dg/python-image.html
# It's also illustrated in the "Creating container images" section of the docs.
# https://docs.aws.amazon.com/lambda/latest/dg/images-create.html#python-3.8

# Note that this is quite big at 620.3 MB
FROM public.ecr.aws/lambda/python:3.8

# The AWS supplied base image sets
# LAMBDA_TASK_ROOT=/var/task
# LAMBDA_RUNTIME_DIR=/var/runtime
# WORKDIR /var/task
# ENTRYPOINT [ "/lambda-entrypoint.sh" ]
#
# See also https://docs.aws.amazon.com/lambda/latest/dg/configuration-envvars.html#configuration-envvars-runtime

# Install the function's dependencies using file requirements.txt
# from your project folder (not needed for simple echo Lambda).

#COPY requirements.txt  .
#RUN  pip3 install -r requirements.txt --target "${LAMBDA_TASK_ROOT}"

# Copy function code
COPY echo.py ${LAMBDA_TASK_ROOT}

# Set the CMD to your handler (could also be done as a parameter override
# outside of the Dockerfile)
CMD ["echo.handler"] 

#-------------------------------------------------------------------------------
# docker build -t echo-lambda-aws-base .

# Run using AWS Lambda RIE (if installed)
# docker run --rm -u 1000:1000 -p 8080:8080 echo-lambda-aws-base

# Connect to a Lambda Runtime API Daemon hosted external to the Lambda container.
# This is mostly useful for testing. If AWS_LAMBDA_RUNTIME_API is set the
# lambda-entrypoint.sh script does: exec python3 -m awslambdaric "$1"
# and does not run Lambda Runtime API Daemon in the container.
# docker run --rm -u 1000:1000 -e AWS_LAMBDA_RUNTIME_API=192.168.0.12:9001 -e AWS_LAMBDA_FUNCTION_NAME=echo-lambda echo-lambda-aws-base

# Use this to bind-mount lambda-runtime-api-daemon
# docker run --rm -u 1000:1000 -p 8080:8080 -v ${PWD}/lambda-runtime-api-daemon:/usr/local/bin/aws-lambda-rie -e AWS_LAMBDA_FUNCTION_NAME=echo-lambda -e AMQP_URI="amqp://$(hostname -I | awk '{print $1}'):5672?connection_attempts=20&retry_delay=10&heartbeat=0" echo-lambda-aws-base


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

