# echo-lambda-nodejs
This is a Lambda Container Image for a Node.js based Lambda providing a simple echo service. This example builds a custom Lambda Container Image.

The Dockerfile is a fair bit more complicated than the equivalent [Python based Lambda Container Image](../echo-lambda), partly because we install Node.js from the [binary archive](https://github.com/nodejs/help/wiki/Installation) to give us maximum control over what is actually installed, but also because `aws-lambda-ric` needs to be built, so the image needs a number of C++ build dependencies.

Installing the [Node.js v18.15.0 LTS](https://nodejs.org/dist/v18.15.0/) binary archive is relatively simple by using curl and piping through tar, we then use npm to install `aws-lambda-ric`. [aws-lambda-ric](https://www.npmjs.com/package/aws-lambda-ric) is the AWS Node.js Runtime Interface Client [Open Sourced on GitHub](https://github.com/aws/aws-lambda-nodejs-runtime-interface-client) by AWS. This implements the client side of the Lambda [Runtime API](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html), allowing users to seamlessly extend arbitrary base images to be Lambda compatible. The Lambda Runtime Interface Client is a lightweight interface that allows runtimes to receive requests from and send responses to the Lambda service.

Lambda Container Images bootstrap by using `aws-lambda-ric` as the container's ENTRYPOINT and use the CMD to specify the Lambda handler. To make it simpler to either directly use `aws-lambda-ric` when the image is deployed to the AWS Lambda Service or to run via the Lambda Runtime API Daemon we provide a simple [lambda-entrypoint.sh](lambda-entrypoint.sh) to use as the container ENTRYPOINT:
```
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
```
This simply checks if the AWS_LAMBDA_RUNTIME_API environment variable has been set, as it will be when deployed to the AWS Lambda Service, and if so starts with:
```
exec node /usr/local/lib/node_modules/.bin/aws-lambda-ric "$1"
```
where the exec means that `node` will replace the lambda-entrypoint.sh process.

If on the other hand the environment variable has not been set the container will start with:
```
exec /usr/local/bin/aws-lambda-rie node /usr/local/lib/node_modules/.bin/aws-lambda-ric "$1"
```
In this case we exec "aws-lambda-rie", which will then run as PID1 in the container.

This approach follows the pattern described in the AWS documentation [Testing Images](https://docs.aws.amazon.com/lambda/latest/dg/images-test.html) section under the heading "Build RIE into your base image" and also described in the [Runtime Interface Emulator](https://github.com/aws/aws-lambda-runtime-interface-emulator/#build-rie-into-your-base-image) documentation.

Note that the executable path `/usr/local/bin/aws-lambda-rie` has been used in `lambda-entrypoint.sh`. That is potentially slightly odd and a little confusing, but the reason is simply because the AWS base images bundle the RIE and they have an ENTRYPOINT that attempts to use `aws-lambda-rie` if AWS_LAMBDA_RUNTIME_API is not set. So, the `lambda-entrypoint.sh` in this example uses that path purely for consistency with the AWS supplied base images. When the container is run the `lambda-runtime-api-daemon` executable is bind-mounted to `/usr/local/bin/aws-lambda-rie`and so it is *actually* `lambda-runtime-api-daemon` that is executed .

Another point of note is that in some of the AWS documentation `aws-lambda-ric` is started by [npx](https://www.npmjs.com/package/npx). We don't do that here for two reasons; The first is that npx with Node.js 16 and 18 doesn't start as cleanly as npx with earlier versions of Node.js, spawning an intermediate shell instead of just running node. The second reason is that by starting `aws-lambda-ric` directly from node we can remove the npm and npx binaries and libraries from the final image as they are now not needed at run time. This approach results in a smaller image with a smaller user space surface.

To build the image:
```
docker build -t echo-lambda-nodejs .
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
  echo-lambda-nodejs

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
