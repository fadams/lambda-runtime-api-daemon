# python-example-wrapper
This is a demo of a Lambda Internal Extension using a custom container image and a wrapper script  written in Python. It is based on the AWS [Example Wrapper Script in Python](https://github.com/aws-samples/aws-lambda-extensions/tree/main/python-example-wrapper) sample. This example is very similar to [bash-example-wrapper](bash-example-wrapper) except that the wrapper script is written in Python rather than bash.

In the original example the wrapper script was deployed as a Lambda Layer, but this repo presents an update of that example packaged as a custom Lambda container image. The [function](lambda_function.py) code is identical to the original version and like that the location of the wrapper script is specified by the `AWS_LAMBDA_EXEC_WRAPPER` environment variable. In this example, however, we are using a custom container image rather than a managed Lambda runtime or an AWS base image.

In the [bash-example-wrapper](bash-example-wrapper) we couldn't use the original [wrapper script](https://github.com/aws-samples/aws-lambda-extensions/blob/main/bash-example-wrapper/wrapper_script) from the example _directly_ because the original script inserted `-X importtime` immediately **before** the last argument, which broke the awslambdaric bootstrapping. Fortunately, however, the original Python [wrapper script](https://github.com/aws-samples/aws-lambda-extensions/blob/main/python-example-wrapper/wrapper_script) for this example works "out of the box" as it inserts `-X importtime` after the first argument.

The Dockerfile is simple, installing python3 and pip3 from the main Ubuntu package repositories and COPYing the the custom wrapper script, the function, and the ENTRYPOINT.

To build the image:
```
docker build -t python-example-wrapper .
```
To run the container locally (N.B. as provided this script expects a RabbitMQ AMQP broker to be available locally, though it is a configurable setting):
```
./python-example-wrapper.sh
```
## Usage
In *real world* usage one would invoke via AWS SDK libraries or AMQP messages, however it is also possible to invoke the Lambda from the command line either by using curl or the AWS CLI.

Use curl with a "generic" Function name: 
```
curl -XPOST "http://localhost:8080/2015-03-31/functions/function/invocations" -d '{"key": "value"}'
```
Use curl with the correct Function name: 
```
curl -XPOST "http://localhost:8080/2015-03-31/functions/python-example-wrapper/invocations" -d '{"key": "value"}'
```
Using the correct function name works with the Lambda Runtime API Daemon but **does not** work with the RIE, which has a hard coded function name of "function".

Use the AWS Lambda CLI to invoke:
```
aws lambda invoke --endpoint-url http://localhost:8080 --function-name function --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null
```
Use AWS Lambda CLI  with the correct Function name:
```
aws lambda invoke --endpoint-url http://localhost:8080 --function-name python-example-wrapper --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null
```