#!/bin/sh
if [ $# -ne 1 ]; then
  echo "entrypoint requires the handler name to be the first argument" 1>&2
  exit 142
fi

if [ -z "${AWS_LAMBDA_RUNTIME_API}" ]; then
  exec /usr/local/bin/aws-lambda-rie python3 -m awslambdaric "$1"
else
  exec python3 -m awslambdaric "$1"
fi

