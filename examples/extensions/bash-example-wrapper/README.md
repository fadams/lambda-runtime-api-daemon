# bash-example-wrapper
This is a demo of a Lambda Internal Extension using a custom container image and a wrapper script. It is based on the AWS [Example Wrapper Script in Bash](https://github.com/aws-samples/aws-lambda-extensions/tree/main/bash-example-wrapper) sample, which is the demo presented in the AWS Lambda developer documentation [Runtime Modification - Wrapper Scripts](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-modify.html#runtime-wrapper).

In the original example the wrapper script was deployed as a Lambda Layer, but this repo presents an update of that example packaged as a custom Lambda container image. The [function](lambda_function.py) code is identical to the original version and like that the location of the wrapper script is specified by the `AWS_LAMBDA_EXEC_WRAPPER` environment variable. In this example, however, we are using a custom container image rather than a managed Lambda runtime or an AWS base image, which _technically_ does not support wrapper scripts.

The reason that custom runtimes do not support wrapper scripts "out of the box" is due to small differences in the way the Runtime Interface Client is "bootstrapped" and the requirement for the container ENTRYPOINT to handle `AWS_LAMBDA_EXEC_WRAPPER`.

To handle the `AWS_LAMBDA_EXEC_WRAPPER` environment variable we add support for it to the [lambda-entrypoint.sh](lambda-entrypoint.sh) script.
```
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
```
Unfortunately, we can't use the [wrapper script](https://github.com/aws-samples/aws-lambda-extensions/blob/main/bash-example-wrapper/wrapper_script) from the example _directly_. The issue is due to differences in the way the Runtime Interface Client is “bootstrapped” in AWS base images and when using awslambdaric. With the AWS images and managed runtimes it is (eventually) started with:
```
/var/lang/bin/python3.8 /var/runtime/bootstrap.py
```
whereas with awslambdaric it is started with:
```
python3 -m awslambdaric "$1"
```
The problem is due to the way the wrapper script inserts the extra options:
```
args=("${args[@]:0:$#-1}" "${extra_args[@]}" "${args[@]: -1}")
```
What this does is to insert `-X importtime` immediately **before** the last argument. Because awslambdaric is started using Python's `-m` option that ends up like this:
```
python3 -m awslambdaric -X importtime "$1"
```
which doesn't work correctly.

The solution is to change that line in the wrapper script to:
```
args=("${args[@]:0:1}" "${extra_args[@]}" "${args[@]:1}")
```
In this case `-X importtime` is inserted immediately **after** the first argument, which ends up like:
```
python3 -X importtime -m awslambdaric "$1"
```
for custom custom container images and also works with AWS base images where it ends up like:
```
/var/lang/bin/python3.8 -X importtime /var/runtime/bootstrap.py
```

The Dockerfile is simple, installing python3 and pip3 from the main Ubuntu package repositories and COPYing the the custom wrapper script, the function, and the ENTRYPOINT.

To build the image:
```
docker build -t bash-example-wrapper .
```
To run the container locally (N.B. as provided this script expects a RabbitMQ AMQP broker to be available locally, though it is a configurable setting):
```
./bash-example-wrapper.sh
```
## Usage
In *real world* usage one would invoke via AWS SDK libraries or AMQP messages, however it is also possible to invoke the Lambda from the command line either by using curl or the AWS CLI.

Use curl with a "generic" Function name: 
```
curl -XPOST "http://localhost:8080/2015-03-31/functions/function/invocations" -d '{"key": "value"}'
```
Use curl with the correct Function name: 
```
curl -XPOST "http://localhost:8080/2015-03-31/functions/bash-example-wrapper/invocations" -d '{"key": "value"}'
```
Using the correct function name works with the Lambda Runtime API Daemon but **does not** work with the RIE, which has a hard coded function name of "function".

Use the AWS Lambda CLI to invoke:
```
aws lambda invoke --endpoint-url http://localhost:8080 --function-name function --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null
```
Use AWS Lambda CLI  with the correct Function name:
```
aws lambda invoke --endpoint-url http://localhost:8080 --function-name bash-example-wrapper --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null
```