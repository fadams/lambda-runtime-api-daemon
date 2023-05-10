# custom-runtime-extension-demo
This is a demo of Lambda extensions using a custom container image and a custom Lambda runtime. It is based on the AWS [Custom Runtime Extension(s) Demo](https://github.com/aws-samples/aws-lambda-extensions/tree/main/custom-runtime-extension-demo) sample, which is the demo presented in the AWS [Building Extensions for AWS Lambda](https://aws.amazon.com/blogs/compute/building-extensions-for-aws-lambda-in-preview/) blog post.

The original example used Lambda zip packaging, so this repo presents an update of that example packaged as a Lambda container image. The [function](function.sh), [runtime/bootstrap](bootstrap) and [extension](extensions) code is identical to the original except for a small tweak to the bootstrap (runtime) script as the original had a hard coded `LAMBDA_TASK_ROOT`, whereas it's preferable to be able to set this from an environment variable, so that line is commented out here.

Note that this example is intended to be purely illustrative as, apart from being written entirely in bash, the runtime, function and extensions all contain numerous sleep commands to illustrate the lifecycle of each component and to simulate real work.

The Dockerfile is simple, installing curl from the main Ubuntu package repositories and COPYing the bash scripts representing the custom runtime, the function, the extensions and the ENTRYPOINT.

To build the image:
```
docker build -t custom-runtime-extension .
```
To run the container locally (N.B. as provided this script expects a RabbitMQ AMQP broker to be available locally, though it is a configurable setting):
```
./custom-runtime-extension.sh
```


## Usage
In *real world* usage one would invoke via AWS SDK libraries or AMQP messages, however it is also possible to invoke the Lambda from the command line either by using curl or the AWS CLI.

Use curl with a "generic" Function name: 
```
curl -XPOST "http://localhost:8080/2015-03-31/functions/function/invocations" -d '{"key": "value"}'
```

Use curl with the correct Function name: 
```
curl -XPOST "http://localhost:8080/2015-03-31/functions/custom-runtime-extension/invocations" -d '{"key": "value"}'
```
Using the correct function name works with the Lambda Runtime API Daemon but **does not** work with the RIE, which has a hard coded function name of "function".

Use the AWS Lambda CLI to invoke:
```
aws lambda invoke --endpoint-url http://localhost:8080 --function-name function --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null
```
Use AWS Lambda CLI  with the correct Function name:
```
aws lambda invoke --endpoint-url http://localhost:8080 --function-name custom-runtime-extension --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null
```