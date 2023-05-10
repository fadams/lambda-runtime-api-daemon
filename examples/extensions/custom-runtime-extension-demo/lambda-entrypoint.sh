#!/bin/sh
if [ $# -ne 1 ]; then
  echo "entrypoint requires the handler name to be the first argument" 1>&2
  exit 142
fi
export _HANDLER="$1"

if [ -z "${AWS_LAMBDA_RUNTIME_API}" ]; then
  exec /usr/local/bin/aws-lambda-rie /bootstrap
else
  exec /bootstrap
fi

