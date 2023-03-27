# echo-lambda-custom-runtime
This is a Lambda Container Image using a [custom Lambda Runtime](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-custom.html) based on the [Custom Runtime Tutorial](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-walkthrough.html) from the AWS documentation that illustrates creating a Lambda using bash and curl.

Note that this example is intended to be purely illustrative, to show that **anything** that can correctly call the Lambda [Runtime API](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html) is a valid Lambda. It is not, however, very efficient to use bash and curl in this way, in particular each Runtime API call results in new curl processes being created and new socket connections being established.

The Dockerfile is simple, installing curl from the main Ubuntu package repositories and COPYing the bash scripts representing the custom runtime, the function and the ENTRYPOINT.

The `bootstrap` script representing the runtime is the most interesting:
```
set -euo pipefail

# Initialization - load function handler
source $LAMBDA_TASK_ROOT/"$(echo $_HANDLER | cut -d. -f1).sh"

# Processing
while true
do
  HEADERS="$(mktemp)"

  # Get an event. The HTTP request will block until one is received
  EVENT_DATA=$(curl -sS -LD "$HEADERS" -X GET "http://${AWS_LAMBDA_RUNTIME_API}/2018-06-01/runtime/invocation/next")

  # Extract request ID by scraping the headers received above
  REQUEST_ID=$(grep -Fi Lambda-Runtime-Aws-Request-Id "$HEADERS" | tr -d '[:space:]' | cut -d: -f2)

  # Run the handler function from the script passing the EVENT_DATA and also
  # the HEADERS dir from the next endpoint response as quick & dirty context.
  RESPONSE=$($(echo "$_HANDLER" | cut -d. -f2) "$EVENT_DATA" "$HEADERS")

  # Send the response
  curl -sS -o /dev/null -X POST "http://${AWS_LAMBDA_RUNTIME_API}/2018-06-01/runtime/invocation/$REQUEST_ID/response"  -d "$RESPONSE"
done
```
The `set -euo pipefail` is a standard bash pattern to cause the script to fail on error.

`source $LAMBDA_TASK_ROOT/"$(echo $_HANDLER | cut -d. -f1).sh"` "loads" the function script. It works by taking the name set in CMD, e.g. `function.handler`, splitting on the dot then appending ".sh" to the first field of the split, so ends up loading `function.sh`.

The remainder of the script runs a loop, first using curl to do a GET on the Runtime API "next" endpoint and extracting the "Lambda-Runtime-Aws-Request-Id" HTTP header used as a correlation ID and needed for the response.

The line `RESPONSE=$($(echo "$_HANDLER" | cut -d. -f2) "$EVENT_DATA" "$HEADERS")` actually invokes the Lambda handler, passing the event and context (in the form of the path to the HTTP headers).

Finally, the script calls curl to POST the response to the Runtime API "response" endpoint.

It is clearly a very basic and not especially useful Lambda Runtime, but hopefully helps to demystify how Lambda actually works.

The `function.sh` is fairly trivial:
```
function handler () {
  EVENT_DATA=$1
  CONTEXT=$2 # Really the HTTP headers from the Runtime API next response.

  RESPONSE="$EVENT_DATA" # For this Lambda simply echo the EVENT_DATA

  echo $RESPONSE
}
```
It could be simplified to:
```
function handler () {
  echo $1
}
```
but the more verbose form does a better job explaining what it is doing.

To build the image:
```
docker build -t echo-lambda-custom-runtime .
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
  echo-lambda-custom-runtime

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

Use curl with a simple string. Note the quoted JSON string. If just "hello" were used here the Lambda should fail to unmarshall because it's not valid JSON.
```
curl -XPOST "http://localhost:8080/2015-03-31/functions/echo-lambda/invocations" -d '"hello"'
```
This **should** fail as the request is not valid JSON, however with this runtime the request succeeds and illustrates one of its limitations. A production-ready Lambda runtime should include error handling and behave consistently with the official AWS runtimes:
```
curl -XPOST "http://localhost:8080/2015-03-31/functions/echo-lambda/invocations" -d "hello"
```
Use the AWS Lambda CLI to invoke:
```
aws lambda invoke --endpoint-url http://localhost:8080 --function-name echo-lambda --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null
```
## Standalone
Although the most common usage pattern for the Lambda Runtime API Daemon is to be embedded or mounted in a Lambda Container Image container it is possible to run it standalone on a Linux host.

From the echo-lambda-custom-runtime directory running:
```
LAMBDA_TASK_ROOT=. ../../../bin/lambda-runtime-api-daemon ./bootstrap function.handler
```
will launch `lambda-runtime-api-daemon` using the relative path to the executable. Here we set LAMBDA_TASK_ROOT to the current directory where bootstrap and function.sh are hosted.

This way of running `lambda-runtime-api-daemon` can be useful during development as it avoids the need to build Container Images.
