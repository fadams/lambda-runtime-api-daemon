# python-example-internal-extension
This demo illustrates a "full" Python Lambda Internal Extension (e.g. one that registers for events as opposed to just a wrapper script) using a custom container image. 

The AWS documentation for *Internal* Extensions that actually register for events is very sparse and the actual use case for doing this is not *immediately* obvious, because all of the information available to the `INVOKE` Extension event (and more) is available to the Lambda handler.

The main use case for Internal Extensions registering is actually mostly to do with graceful shutdown, though that is far from clear from the AWS documentation and moreover the actual usage pattern required isn't obvious.

The background of this is that when Lambda Runtimes are idle the Lambda Service may arbitrarily remove idle instances. If no Extensions are registered the Runtime instances are forcibly killed via SIGKILL. Now SIGKILL cannot be blocked nor handled by user processes, so applications that use Lambda to connect with external services like databases could inadvertently keep those resources open requiring them to rely on timeouts being managed on idle connections.

If, however, Extensions are registered Lambda supports graceful shutdown. When the Lambda service is about to shut down an idle Runtime, it sends a SIGTERM signal to the Runtime and a `SHUTDOWN` event to each registered External Extension. Developers can catch the SIGTERM signal in their Lambda functions and clean up resources. This is *somewhat* mentioned in the Extensions API [Shutdown phase](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-extensions-api.html#runtimes-lifecycle-shutdown) documentation and a bit more explicitly in some [aws-samples](https://github.com/aws-samples/graceful-shutdown-with-aws-lambda), though all of that documentation only talks about graceful shutdown of Lambdas with External Extensions.

The only place in the main Lambda documentation shutdown is mentioned in the context of Internal Extensions is the [Adding a shutdown hook to the JVM runtime process](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-modify.html#runtimes-envvars-ex2) example, where it says:
```
import java.lang.instrument.Instrumentation;

public class Agent {

    public static void premain(String agentArgs, Instrumentation inst) {
//      Register the extension.
//      ...

//      Register the shutdown hook
        addShutdownHook();
    }

    private static void addShutdownHook() {
//      Shutdown hooks get up to 500 ms to handle graceful shutdown before the runtime is terminated.
//
//      You can use this time to egress any remaining telemetry, close open database connections, etc.
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
//            Inside the shutdown hook's thread we can perform any remaining task which needs to be done.
        }));
    }
}
```
but gives no information about "Register the extension", so it is easily missed.

Another "quirk" is that Internal extensions are started and stopped by the Runtime process, so they are not permitted to register for the `SHUTDOWN` event. Internal Extensions **can**, however, send an empty events list in the register request, e.g. `{"events": []}`, which will ensure that the Runtime is sent SIGTERM.

Unfortunately for the case of Python there is another quirk that renders this feature useless and makes this example vaguely pointless except as an illustration. The issue is due to the way Python handles signals, where the underlying native signal handler sets a flag that is only dealt with when control returns to the Python interpreter. In most cases this approach works reasonably, but in cases where control is blocked on native code signals may not be correctly propagated. For Lambda the Python Runtimes up to Python 3.7 used the requests library to connect to the Runtime API, so signal handlers will work. For Python 3.8 onwards the Runtime Interface Client uses C++ based native code to perform the HTTP connection and as the call to the "next" Runtime API endpoint blocks until a new invocation event is available this means that the SIGTERM sent to the Python Runtime is never propagated to any user signal handlers. The AWS [Graceful shutdown with AWS Lambda](https://github.com/aws-samples/graceful-shutdown-with-aws-lambda) example states "Please be aware that this feature does not work on Python 3.8 and 3.9 runtimes", but doesn't explain why.

The Dockerfile is simple, installing python3 and pip3 from the main Ubuntu package repositories and COPYing  the function and the ENTRYPOINT. The dependencies for the Extension are installed by the line:
```
# Install Extension dependencies
pip3 install -r ${LAMBDA_TASK_ROOT}/requirements.txt && \
```

To build the image:
```
docker build -t python-example-internal-extension .
```
To run the container locally (N.B. as provided this script expects a RabbitMQ AMQP broker to be available locally, though it is a configurable setting):
```
./python-example-internal-extension.sh
```
## Usage
In *real world* usage one would invoke via AWS SDK libraries or AMQP messages, however it is also possible to invoke the Lambda from the command line either by using curl or the AWS CLI.

Use curl with a "generic" Function name: 
```
curl -XPOST "http://localhost:8080/2015-03-31/functions/function/invocations" -d '{"key": "value"}'
```
Use curl with the correct Function name: 
```
curl -XPOST "http://localhost:8080/2015-03-31/functions/python-example-internal-extension/invocations" -d '{"key": "value"}'
```
Using the correct function name works with the Lambda Runtime API Daemon but **does not** work with the RIE, which has a hard coded function name of "function".

Use the AWS Lambda CLI to invoke:
```
aws lambda invoke --endpoint-url http://localhost:8080 --function-name function --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null
```
Use AWS Lambda CLI  with the correct Function name:
```
aws lambda invoke --endpoint-url http://localhost:8080 --function-name python-example-internal-extension --cli-binary-format raw-in-base64-out --payload '{ "key": "value" }' /dev/stderr 1>/dev/null
```