#!/bin/sh
if [ $# -ne 1 ]; then
  echo "entrypoint requires the handler name to be the first argument" 1>&2
  exit 142
fi

# Check AWS_LAMBDA_EXEC_WRAPPER, if not set there is no wrapper script
if [ -z "$AWS_LAMBDA_EXEC_WRAPPER" ]; then
  # If AWS_LAMBDA_RUNTIME_API is not set then launch using rie/rapid
  if [ -z "${AWS_LAMBDA_RUNTIME_API}" ]; then
    exec /usr/local/bin/aws-lambda-rie python3 -m awslambdaric "$1"
  else
    exec python3 -m awslambdaric "$1"
  fi
else
  wrapper="$AWS_LAMBDA_EXEC_WRAPPER"
  if [ ! -f "$wrapper" ]; then
    echo "$wrapper: does not exist"
    exit 127
  fi
  if [ ! -x "$wrapper" ]; then
    echo "$wrapper: is not an executable"
    exit 126
  fi

  # If AWS_LAMBDA_RUNTIME_API is not set then launch using rie/rapid
  if [ -z "${AWS_LAMBDA_RUNTIME_API}" ]; then
    exec /usr/local/bin/aws-lambda-rie "$wrapper" python3 -m awslambdaric "$1"
  else
    exec -- "$wrapper" python3 -m awslambdaric "$1"
  fi
fi
