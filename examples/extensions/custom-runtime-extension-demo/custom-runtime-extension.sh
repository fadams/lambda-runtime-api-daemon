#!/bin/bash

# Copy the lambda-runtime-api-daemon binary so we can bind mount in docker run
# Note that we bind mount to /usr/local/bin/aws-lambda-rie as most of the
# AWS examples use the RIE and the AWS base images have an ENTRYPOINT already
# set to use that executable. We could mount to any arbitrary path, but then
# the container ENTRYPOINT would need to be set to use that. In other words
# its simpler, though not necessary to "pretend" to be the RIE.
cp ${PWD}/../../../bin/lambda-runtime-api-daemon .

# Uses default MAX_CONCURRENCY of 1 and AWS_LAMBDA_FUNCTION_TIMEOUT of 3
# Use environment variables in docker run to adjust e.g.
#    -e MAX_CONCURRENCY=10 \
#    -e AWS_LAMBDA_FUNCTION_TIMEOUT=60 \

docker run --rm  \
    -u $(id -u):$(id -g) \
    -p 8080:8080 \
    -e AWS_LAMBDA_FUNCTION_TIMEOUT=10 \
    -v ${PWD}/lambda-runtime-api-daemon:/usr/local/bin/aws-lambda-rie \
    -e AWS_LAMBDA_FUNCTION_NAME=custom-runtime-extension \
    -e AMQP_URI="amqp://$(hostname -I | awk '{print $1}'):5672?connection_attempts=20&retry_delay=10&heartbeat=0" \
    custom-runtime-extension

rm lambda-runtime-api-daemon

