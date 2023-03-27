# echo-lambda
This is a Lambda Container Image for a Python based Lambda providing a simple echo service. This example builds a custom Lambda Container Image.

The Dockerfile is very simple, installing the `python3` and `python3-pip` packages from the main Ubuntu package repositories then using pip to install `awslambdaric`. [awslambdaric](https://pypi.org/project/awslambdaric/) is the AWS Python Runtime Interface Client [Open Sourced on GitHub](https://github.com/aws/aws-lambda-python-runtime-interface-client) by AWS. This implements the client side of the Lambda [Runtime API](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html), allowing users to seamlessly extend arbitrary base images to be Lambda compatible. The Lambda Runtime Interface Client is a lightweight interface that allows runtimes to receive requests from and send responses to the Lambda service.

Lambda Container Images bootstrap by using `awslambdaric` as the container's ENTRYPOINT and use the CMD to specify the Lambda handler. To make it simpler to either directly use `awslambdaric` when the image is deployed to the AWS Lambda Service or to run via the Lambda Runtime API Daemon we provide a simple [lambda-entrypoint.sh](lambda-entrypoint.sh) to use as the container ENTRYPOINT:
```
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
```
This simply checks if the AWS_LAMBDA_RUNTIME_API environment variable has been set, as it will be when deployed to the AWS Lambda Service, and if so starts with:
```
exec python3 -m awslambdaric "$1"
```
where the exec means that `awslambdaric` will replace the lambda-entrypoint.sh process.

If on the other hand the environment variable has not been set the container will start with:
```
exec /usr/local/bin/aws-lambda-rie python3 -m awslambdaric "$1"
```
In this case we exec "aws-lambda-rie", which will then run as PID1 in the container.

This approach follows the pattern described in the AWS documentation [Testing Images](https://docs.aws.amazon.com/lambda/latest/dg/images-test.html) section under the heading "Build RIE into your base image" and also described in the [Runtime Interface Emulator](https://github.com/aws/aws-lambda-runtime-interface-emulator/#build-rie-into-your-base-image) documentation.

Note that the executable path `/usr/local/bin/aws-lambda-rie` has been used in `lambda-entrypoint.sh`. That is potentially slightly odd and a little confusing, but the reason is simply because the AWS base images bundle the RIE and they have an ENTRYPOINT that attempts to use `aws-lambda-rie` if AWS_LAMBDA_RUNTIME_API is not set. So, the `lambda-entrypoint.sh` in this example uses that path purely for consistency with the AWS supplied base images. When the container is run the `lambda-runtime-api-daemon` executable is bind-mounted to `/usr/local/bin/aws-lambda-rie`and so it is* actually* `lambda-runtime-api-daemon` that is executed .

To build the image:
```
docker build -t echo-lambda .
```
To run the echo Lambda container locally (N.B. as provided this script expects a RabbitMQ AMQP broker to be available locally, though it is a configurable setting):
```
./echo-lambda.sh
```
The script is very simple and copies `lambda-runtime-api-daemon` from the bin directory then does a `docker run` with flags set to bind-mount `lambda-runtime-api-daemon` to `/usr/local/bin/aws-lambda-rie` and sets the container user, exported port and the AWS_LAMBDA_FUNCTION_NAME and AMQP_URI environment variables.

The full script is:
```
cp ${PWD}/../../../bin/lambda-runtime-api-daemon .

docker run --rm  \
  -u $(id -u):$(id -g) \
  -p 8080:8080 \
  -v ${PWD}/lambda-runtime-api-daemon:/usr/local/bin/aws-lambda-rie \
  -e AWS_LAMBDA_FUNCTION_NAME=echo-lambda \
  -e AMQP_URI="amqp://$(hostname -I | awk '{print $1}'):5672?connection_attempts=20&retry_delay=10&heartbeat=0" \
  echo-lambda

rm lambda-runtime-api-daemon
```
If the AMQP_URI environment variable is omitted the Lambda Runtime API Daemon AMQP-RPC endpoint is disabled and only the HTTP endpoint will be exposed. When the Lambda Runtime API Daemon is deployed with the AMQP-RPC endpoint disabled it behaves very much like an enhanced RIE and requires no external dependencies.

The AWS_LAMBDA_FUNCTION_NAME environment variable represents the Function Name that would be used to invoke the Lambda via the Invoke API, e.g. the FunctionName of the Invoke URI:
```
/2015-03-31/functions/FunctionName/invocations?Qualifier=Qualifier
```
it is also used as the AMQP queue name and on startup the Lambda Runtime API Daemon will declare a queue named after the Function Name if one doesn't already exist.

By default the Lambda has a timeout of three seconds, which is the default value of the AWS Lambda Service, it may be configured by setting the AWS_LAMBDA_FUNCTION_TIMEOUT environment variable.

By default the Lambda concurrency is based on the number of instances of the execution environment that are running, e.g. the number of container instances. In this scenario multiple instances bind to the AMQP queue and messages are delivered to each instance in a fair manner. In addition it is possible to set the MAX_CONCURRENCY of each instance. In this case additional Lambda processes are spawned on demand inside each execution environment which can give lower latency and higher throughput for some more IO bound workloads.

## Usage
[echo-clients](../echo-clients) are provided to illustrate how to invoke the Lambda using the AWS SDK and via AMQP, however it is also possible to invoke the Lambda from the command line either by using curl or the AWS CLI.

Use curl with a "generic" Function name: 
```
curl -XPOST "http://localhost:8080/2015-03-31/functions/function/invocations" -d '{"key": "value"}'
```

Use curl with the correct Function name: 
```
curl -XPOST "http://localhost:8080/2015-03-31/functions/echo-lambda/invocations" -d '{"key": "value"}'
```
Using the correct function name works with the Lambda Runtime API Daemon but **does not** work with the RIE, which has a hard coded function name of "function".

Use curl with a simple string. Note the quoted JSON string. If just "hello" were used here the Lambda will fail to unmarshall because it's not valid JSON.
```
curl -XPOST "http://localhost:8080/2015-03-31/functions/echo-lambda/invocations" -d '"hello"'
```
This will fail as the request is not valid JSON:
```
curl -XPOST "http://localhost:8080/2015-03-31/functions/echo-lambda/invocations" -d "hello"
```
Use the AWS Lambda CLI to invoke:
```
aws lambda invoke --endpoint-url http://localhost:8080 --function-name echo-lambda --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null
```

## Standalone
Although the most common usage pattern for the Lambda Runtime API Daemon is to be embedded or mounted in a Lambda Container Image container it is possible to run it standalone on a Linux host.

If `awslambdaric` is installed locally e.g. by running `pip3 install awslambdaric`then from the echo-lambda directory running:
```
../../../bin/lambda-runtime-api-daemon python3 -m awslambdaric echo.handler
```

will launch `lambda-runtime-api-daemon` using the relative path to the executable.

This way of running `lambda-runtime-api-daemon` can be useful during development as it avoids the need to build Container Images.