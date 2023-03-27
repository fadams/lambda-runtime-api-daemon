# echo-lambda-aws-base
This is a Lambda Container Image for a Python based Lambda providing a simple echo service. This example uses the AWS supplied Python [Lambda base image](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-images.html).

The Dockerfile is extremely simple, taking the AWS base image and using COPY to add the Lambda handler for the echo function and setting CMD to specify the Lambda handler to the Lambda bootstrap.

With AWS supplied base images we don't need to explicitly install `awslambdaric`nor add an ENTRYPOINT to selectively use `aws-lambda-rie` if AWS_LAMBDA_RUNTIME_API is not set as those are already set.

To build the image:
```
docker build -t echo-lambda-aws-base .
```
Note that this image is *significantly* larger that the equivalent [echo-lambda](../echo-lambda) image built using a custom Lambda Container Image. It is also slightly slower at run time, the reason for that is not clear but is most likely due to using a slightly older and less efficient version of `awslambdaric`.

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
  echo-lambda-aws-base

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