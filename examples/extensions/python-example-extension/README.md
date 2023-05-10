# python-example-extension
This is a demo of a Lambda External Extension written in Python using a custom container image. It is based on the AWS [Example Extension in Python](https://github.com/aws-samples/aws-lambda-extensions/tree/main/python-example-extension) sample.

In the original example the Extension was deployed as a Lambda Layer, but this repo presents an update of that example packaged as a custom Lambda container image. The original example only contained the Extension, but here we include a simple "Hello from Lambda" [function](lambda_function.py) so we can build a complete functioning container image.

Note that the [extensions](extensions) directory contains [python-example-extension](extensions/python-example-extension) which is a bash file that execs the actual Python Extension. This is exactly the same approach used in the original example and is because Lambda External Extensions **must** be deployed as files in /opt/extensions and the filename of the Extensions **must** match the name supplied when registering the Extension.

Note too that the Extension code is presented here unchanged from the original [example](https://github.com/aws-samples/aws-lambda-extensions/tree/main/python-example-extension) and contains some inefficiencies, notably using the requests HTTP library, which uses blocking rather than asyncio, and also a simple `requests.get` call is used:
```
response = requests.get(
  url=f"http://{os.environ['AWS_LAMBDA_RUNTIME_API']}/2020-01-01/extension/event/next",
  headers=headers,
  timeout=None
)
```
Using the requests global functions in this way results in a new TCP connection being created for each HTTP get on the /extension/event/next endpoint rather than reusing the underlying connection as is the case with the awslambdaric Runtime Interface Client.

To resolve this, a better approach would be to use a requests [Session()](https://requests.readthedocs.io/en/latest/user/advanced/) by creating a session instance in the Extention init, e.g. replacing the lines:
```
# global variables
# extension name has to match the file's parent directory name)
LAMBDA_EXTENSION_NAME = Path(__file__).parent.name
```
with
```
# global variables
# extension name has to match the file's parent directory name)
LAMBDA_EXTENSION_NAME = Path(__file__).parent.name

# Create a requests Session so that the underlying
# TCP connection is reused
# https://requests.readthedocs.io/en/latest/user/advanced/
session = requests.Session()
```
then using session in place of the global requests object, e.g. 
```
response = session.get(
  url=f"http://{os.environ['AWS_LAMBDA_RUNTIME_API']}/2020-01-01/extension/event/next",
  headers=headers,
  timeout=None
)
```

The Dockerfile is simple, installing python3 and pip3 from the main Ubuntu package repositories and COPYing  the function, Extension and the ENTRYPOINT. The dependencies for the Extension are installed by the line:
```
# Install Extension dependencies
pip3 install -r /opt/python-example-extension/requirements.txt && \
```

To build the image:
```
docker build -t python-example-extension .
```
To run the container locally (N.B. as provided this script expects a RabbitMQ AMQP broker to be available locally, though it is a configurable setting):
```
./python-example-extension.sh
```
## Usage
In *real world* usage one would invoke via AWS SDK libraries or AMQP messages, however it is also possible to invoke the Lambda from the command line either by using curl or the AWS CLI.

Use curl with a "generic" Function name: 
```
curl -XPOST "http://localhost:8080/2015-03-31/functions/function/invocations" -d '{"key": "value"}'
```
Use curl with the correct Function name: 
```
curl -XPOST "http://localhost:8080/2015-03-31/functions/python-example-extension/invocations" -d '{"key": "value"}'
```
Using the correct function name works with the Lambda Runtime API Daemon but **does not** work with the RIE, which has a hard coded function name of "function".

Use the AWS Lambda CLI to invoke:
```
aws lambda invoke --endpoint-url http://localhost:8080 --function-name function --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null
```
Use AWS Lambda CLI  with the correct Function name:
```
aws lambda invoke --endpoint-url http://localhost:8080 --function-name python-example-extension --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null
```