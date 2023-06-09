#!/bin/sh
if [ $# -ne 1 ]; then
  echo "entrypoint requires the handler name to be the first argument" 1>&2
  exit 142
fi

if [ -z "${AWS_LAMBDA_RUNTIME_API}" ]; then
  #exec /usr/local/bin/aws-lambda-rie /usr/local/bin/npx aws-lambda-ric "$1"
  exec /usr/local/bin/aws-lambda-rie node /usr/local/lib/node_modules/.bin/aws-lambda-ric "$1"
else
  #exec /usr/local/bin/npx aws-lambda-ric "$1"
  exec node /usr/local/lib/node_modules/.bin/aws-lambda-ric "$1"
fi

