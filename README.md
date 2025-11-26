![lambda-runtime-api-daemon](diagrams/lambda-runtime-api-daemon.png) 

The Lambda Runtime API Daemon (RAPID) and its associated optional Lambda Server allows AWS Lambda functions packaged as [Lambda Container Images](https://docs.aws.amazon.com/lambda/latest/dg/gettingstarted-package.html#gettingstarted-package-images) to be deployed and run in a wide range of heterogenous compute environments in addition to the proprietary AWS Lambda Service, for example as Docker images or in Kubernetes, [OpenFaaS](examples/kubernetes/kind-openfaas) or even standalone on a Linux host.

## Motivation
From its introduction in November 2014 [AWS Lambda](https://docs.aws.amazon.com/lambda/) has been a game changing service, mainstreaming the idea of [Function as a Service (FaaS)](https://en.wikipedia.org/wiki/Function_as_a_service) as a microservice development and deployment pattern.

In many cases fully embracing the managed AWS Lambda ecosystem and deploying Lambdas to the AWS Lambda Service is entirely the sensible thing to do, however there are a number of scenarios where deploying Lambdas to other, more heterogenous, compute infrastructure offers advantages. Some examples include:

* High Compute, Memory, or execution Duration requirements: AWS Lambda is flexible and the compute and memory capacity available to functions has grown steadily since Lambda was first introduced, but those constraints and the maximum [Function timeout of 900 seconds (15 minutes)](https://docs.aws.amazon.com/lambda/latest/dg/gettingstarted-limits.html) can be inconvenient. Being able to use the same FaaS Programming Model, APIs, tooling, CI/CD, etc. for all compute, but being able to deploy to a heterogenous deployment environment can be extremely useful.

* GPU or other accelerator requirements: AWS Lambda does not yet support any compute acceleration other than [AVX2 SIMD instructions](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-avx2.html), but as with the other more general high compute requirements covered in the previous bullet, being able to use the same Lambda Programming Model for all heterogenous compute is useful.

* Hybrid cloud deployment environments: The AWS Lambda _Service_ is only available on AWS, but there are a wide range of Cloud vendors available and on-prem Cloud-like infrastructure is widespread. AWS Lambda _Concepts_ are powerful and compelling, but so is fear of potential Vendor Lock-in and the Lambda Runtime API Daemon can mitigate this.

* FINOPS: Whilst the AWS Lambda Service is relatively cost-effective there are a number of cases like "always on" applications and lots of high compute or long duration workloads where it will end up being more cost-effective to deploy to a different compute service like EC2, Fargate or even on-prem. Making effective FINOPS based decisions is an important part of being an intelligent Cloud consumer, but being able to make these decisions purely on the basis of picking the most cost effective compute for a given workload can be hard as it usually requires a significant amount of redesign. Using the Lambda Programming Model for all compute requirements but being able to _deploy_ that compute in a relatively heterogenous way provides a high level of flexibility.

* Edge/IoT/Scale-Down: When we talk about scaling we usually think of scaling up, but when we build Cloud Native services we shouldn't forget that they might also need to be deployed to Edge environments in small form factor servers or devices. Designing a system using the Lambda Programming Model shouldn't have to mean that it is only deployable to the AWS Lambda Service.

* Testing: Testing Lambda functions locally can help productivity and reduce friction. The Lambda Runtime API Daemon allows users to locally test Lambda functions packaged as Lambda Container Images.

## Architecture
The Lambda Runtime API Daemon is somewhat _"inspired by"_ the [AWS Lambda Runtime Interface Emulator (RIE)](https://github.com/aws/aws-lambda-runtime-interface-emulator). The RIE is described in the [Testing images](https://docs.aws.amazon.com/lambda/latest/dg/images-test.html) section of the AWS Lambda documentation and the original intention was to use the RIE as a starting point for the Lambda Runtime API Daemon. In practice, however, the RIE is quite limited especially with respect to concurrency and integrating additional invocation triggers like AMQP-RPC, so ultimately it was simpler to start afresh from the perspective of the aims of the Lambda Runtime API Daemon and just use the RIE as inspiration.

To understand The Lambda Runtime API Daemon and its associated Lambda Server it is useful to consider the full context of AWS Lambda with this diagram derived from the AWS [Lambda Execution Environment](https://docs.aws.amazon.com/lambda/latest/dg/lambda-runtime-environment.html) documentation.

![lambda-architecture](diagrams/lambda-architecture.png) 

### Execution Environment
The Execution Environment provides _a secure and isolated runtime environment_ and is _conceptually_ a container. In the context of AWS Lambda the actual Execution Environment is an "implementation detail" over which customers have no control nor influence. When Lambda was first introduced in 2014 it was implemented as a proprietary linux container similar to (but not) Docker, with the Execution Environments for each tenant hosted in their own pool of dedicated EC2 VMs. More recently, however, AWS changed approach to use Firecracker MicroVMs to host each Execution Environment instance as described in the [Security Overview Whitepaper](https://docs.aws.amazon.com/pdfs/whitepapers/latest/security-overview-aws-lambda/security-overview-aws-lambda.pdf).

From the perspective of this Lambda Runtime API Daemon implementation the Execution Environment is relatively unimportant, it will commonly be a Docker container, but may easily be deployed to a Firecracker root filesystem or indeed run directly on a Linux host.

### Processes
the _"Function"_ referred to in the above diagram is what users might normally think of as "a Lambda". The [Lambda Programming Model](https://docs.aws.amazon.com/lambda/latest/dg/foundation-progmodel.html) defines the interface between application code and the Lambda system. Users specify the entry point to a Function by defining a handler in the Function configuration. The runtime passes objects to the handler that contain the invocation event and the context, such as the Function name and request ID, and when the handler finishes processing each event, the runtime sends it another.

In this context the [Lambda Runtime](https://docs.aws.amazon.com/lambda/latest/dg/lambda-runtimes.html) is a language-specific environment that relays invocation events, context information, and responses between the Lambda Runtime API and the Function. A valid Lambda Runtime can be _anything_ that acts as a client of the Lambda [Runtime API](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html) and it is entirely possible to [write a Lambda using bash and curl](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-walkthrough.html), however in most cases users will use AWS Open Source [Runtime Interface Clients](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-images.html#runtimes-api-client). Runtime Interface Clients (RIC) are available for each of the supported Lambda runtimes including [Node.js](https://docs.aws.amazon.com/lambda/latest/dg/nodejs-image.html#nodejs-image-clients), [Python](https://docs.aws.amazon.com/lambda/latest/dg/python-image.html#python-image-clients), [Java](https://docs.aws.amazon.com/lambda/latest/dg/java-image.html#java-image-clients), [.NET](https://docs.aws.amazon.com/lambda/latest/dg/csharp-image.html#csharp-image-clients), [go](https://docs.aws.amazon.com/lambda/latest/dg/go-image.html#go-image-clients) and [Ruby](https://docs.aws.amazon.com/lambda/latest/dg/ruby-image.html#ruby-image-clients) and would typically be included in a Lambda Container Image and invoked as the ENTRYPOINT.

### API Endpoints
The role of the Lambda Runtime API Daemon is to implement the API Endpoints part of the overall Lambda ecosystem. From the perspective of a Lambda _Function_ the Lambda Runtime API Daemon implements the Lambda [Runtime API](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html) which is an HTTP API that the Runtime Interface Clients connect to to receive invocation events from Lambda and send response data back within the Lambda Execution Environment. The Lambda Runtime API Daemon will also, in due course, implement the Lambda [Extensions API](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-extensions-api.html) and potentially the [Telemetry API](https://docs.aws.amazon.com/lambda/latest/dg/telemetry-api.html) though for now the focus is on the Runtime API.

Whilst the Lambda _Function_ connects to the Lambda Runtime API Daemon from inside the Execution Environment via the Runtime Interface Client and Runtime API, systems external to the Execution Environment can communicate with the Runtime API Daemon in a number of different ways. The invocation interfaces currently implemented are the Lambda [Invoke API](https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html) and [AMQP-RPC](https://www.rabbitmq.com/tutorials/tutorial-six-python.html).

Note that the Invocation API to any _given_ Execution Environment relates to a single Function, so where the [Invoke API](https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html) defines the URI route:
```
/2015-03-31/functions/FunctionName/invocations?Qualifier=Qualifier
```
each Execution Environment encapsulates a single Function, so the API will expose a single _FunctionName_.

For the case of the AMQP-RPC endpoint the FunctionName of a given Function maps to an AMQP queue.

### Lambda Service
The previous sections explained that the API Endpoints and Processes within an Execution Environment together execute a single Function and expose its invocation endpoints.

The AWS _Lambda Service_ on the other hand provides the means to deploy Functions and exposes the [Invoke API](https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html) in exactly the same way as the Invoke API is exposed by the Execution Environment API Endpoints, but in the case of the Lambda Service when different FunctionNames are passed to the Invoke URI it causes the request to be routed to an Execution Environment instance appropriate to each invoked Function.

For the Lambda Runtime API Daemon the optional Lambda Server performs a somewhat similar role to the Lambda Service Invoke API, where it acts as an [Invoke API](https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html) endpoint and routes requests to each invoked Function.

The reason the Lambda Server is optional is because if AMQP-RPC is used as the Invocation API transport then the routing is performed entirely by the AMQP broker (e.g. RabbitMQ). With AMQP-RPC invocations the FunctionNames correspond to AMQP queues and the AMQP request messages simply specify the FunctionName as the AMQP Routing Key when publishing the message to the default direct exchange, which in turn will deliver the message to the appropriate queue. For the case of the HTTP [Invoke API](https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html) the Lambda Server component simply behaves as an HTTP to AMQP proxy and will extract the FunctionName from the HTTP request URI and publish the request as an AMQP message using the extracted FunctionName as the Routing Key.

Using a messaging service like AMQP as a "Function router" offers a number of advantages, including being relatively "transparent" as it requires no registration mechanism. When a Function is deployed it declares a queue with the broker if one doesn't already exist and multiple instances can share a queue for simple load balancing. Using a messaging service also provides a degree of elastic request buffering and provides the queueing needed by [asynchronous Lambda invocations](https://docs.aws.amazon.com/lambda/latest/dg/invocation-async.html).

In practice the Lambda Server is a relatively simple proxy/gateway component and any number of different routing mechanisms could be employed, from pure HTTP routing via a reverse proxy or API Gateway (which would require some configuration to map `/2015-03-31/functions/FunctionName` URI prefixes to each Function's HTTP [Invoke API](https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html) endpoint), other messaging fabrics, gRPC, etc.

### Lambda Runtime API Daemon with Lambda Server 
The following diagram shows the Function invocation path through the Lambda Runtime API Daemon and Lambda Server together in context, noting again that for clients using only AMQP-RPC the Lambda Server component is optional.

![rapid-invocations-implementation](diagrams/rapid-invocations-implementation.png) 

## Getting Started
### Quick Start
The first step is to build The Lambda Runtime API Daemon and Lambda Server.

If go is installed locally the following instructions should be sufficient:
```
make dep
make
```
alternatively, a Docker based build system is available:
```
make docker
```
In both cases this will build static binaries for the `lambda-runtime-api-daemon` and `lambda-server` and place them in the `bin`directory.

For the Docker based build this will, in addition, create `lambda-runtime-api-daemon` and `lambda-server` Docker images. These images may be executed, however for the Lambda Runtime API Daemon in particular the normal usage pattern is to either include/embed the `lambda-runtime-api-daemon` static binary in the Lambda Container Image or to mount it in the Lambda container at run time as the container is started, so that image is most useful in a Kubernetes context where it is used in [init Containers](https://kubernetes.io/docs/concepts/workloads/pods/init-containers/) as a means to mount the Daemon binary in the Lambda containers.

The Lambda Runtime API Daemon can be used in place of the [AWS Lambda Runtime Interface Emulator (RIE)](https://github.com/aws/aws-lambda-runtime-interface-emulator), so much of the AWS documentation for [Creating container images](https://docs.aws.amazon.com/lambda/latest/dg/images-create.html) and [Testing Lambda container images locally](https://docs.aws.amazon.com/lambda/latest/dg/images-test.html) applies too and the `lambda-runtime-api-daemon` executable can be used as a direct replacement for the `aws-lambda-rie` executable described in the AWS documentation.

Some more concrete usage examples are included in the [examples](examples) directory and a good place to start is the example [echo lambda and clients](examples/echo).

### Deploying Lambdas to Kubernetes
The example [kind-lambda](examples/kubernetes/kind-lambda) illustrates the [Lambda Runtime API Daemon with Lambda Server](#lambda-runtime-api-daemon-with-lambda-server) scenario described earlier.

The example illustrates standing up a local Kubernetes cluster with [kind](https://kind.sigs.k8s.io/) then deploying the Lambda Server and example Lambda Container Images to the cluster, using init Containers and Volumes to mount the lambda-runtime-api-daemon executable so we can use unmodified Lambda Container Images. This example also illustrates a range of different ways to scale the Lambdas using Kubernetes [Horizontal Pod Autoscaling](https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/) or the Runtime API Daemon's own MAX_CONCURRENCY setting.

### Deploying Lambdas to OpenFaaS
The Lambda Runtime API Daemon can be configured to emulate the OpenFaaS [watchdog](https://github.com/openfaas/of-watchdog) API simply by setting the environment variable `ENABLE_OPENFAAS` to `true`. This exposes the additional routes `/`, `/_/health`, and `/_/ready` on the Invoke API allowing unmodified Lambda Container Images to be deployed as OpenFaaS Functions.

The example [kind-openfaas](examples/kubernetes/kind-openfaas) illustrates standing up a local Kubernetes cluster with [kind](https://kind.sigs.k8s.io/) then deploying [OpenFaaS](https://www.openfaas.com/) to it and deploying Lambda Container Images bundled with the Runtime API Daemon configured to act as an OpenFaaS Function watchdog.

## Configuration
The primary means of configuring the Lambda Runtime API Daemon and Lambda Server is through environment variables.

#### Lambda Runtime API Daemon
Currently supported environment variables specific to the Lambda Runtime API Daemon are:

* LOG_LEVEL: The log level (PANIC FATAL ERROR WARNING INFO DEBUG TRACE). The level is case insensitive and the default value is INFO.
* PRINT_REPORTS: Print start and end duration and billed duration reports. Value is case insensitive TRUE or FALSE and the default value is FALSE.
* INVOKE_API_HOST: The Invoke API address, default is 0.0.0.0.
* PORT: The Invoke API listen port, default is 8080.
* RUNTIME_API_HOST: The Runtime API address, default is 127.0.0.1.
* RUNTIME_API_PORT: The Runtime API listen port, default is 9001.
* AMQP_URI: The connection URI to an AMQP message broker (e.g. RabbitMQ). The default is unset, which disables AMQP-RPC support. When set the URI is of the format `amqp://username:password@host:port/virtual_host?key=value&key=value`, e.g. `amqp://guest:guest@localhost:5672?connection_attempts=20&retry_delay=10&heartbeat=0`.
* AMQP_USERNAME: Username for connecting to AMQP broker. Used if not already set in AMQP_URI and if AMQP_PASSWORD is also set.
* AMQP_PASSWORD: Password for connecting to AMQP broker. Used if not already set in AMQP_URI and if AMQP_USERNAME is also set.
* MAX_CONCURRENCY: The maximum number of Lambda instances that may be launched *by this Runtime API Daemon instance*.
* ENABLE_OPENFAAS: If set to true the additional routes `/`, `/_/health`, and `/_/ready` will be exposed by the Invoke API, which will make the Daemon emulate the OpenFaaS [watchdog](https://github.com/openfaas/of-watchdog) API allowing unmodified Lambda Container Images to be deployed as OpenFaaS Functions. Value is case insensitive TRUE or FALSE and the default value is FALSE.

The normal usage pattern is for a Runtime API Daemon to be packaged with a Lambda Container Image or mounted in the container to act as the container ENTRYPOINT. When a Lambda is invoked the Runtime API Daemon execs an instance of the Lambda Runtime API Client, which in turn inits the Lambda and calls the function handler. Each Runtime API Daemon instance is capable of launching and managing multiple Lambda instances in the container, configured by MAX_CONCURRENCY. 

Note that it is also possible to deploy multiple containers, so the aggregate concurrency will be the sum of MAX_CONCURRENCY for all deployed containers. Default value is 1, which means that by default the concurrency is entirely down to how many containers are deployed.

The Lambda Runtime API Daemon also supports a number of Lambda environment variables as described in the [AWS documentation](https://docs.aws.amazon.com/lambda/latest/dg/configuration-envvars.html).

* AWS_LAMBDA_FUNCTION_NAME: The name of the Lambda function, corresponding to the supplied name or the name part of function ARN or partial ARN as described in the invoke API. If not set AWS_LAMBDA_FUNCTION_NAME will default to the "package/module" part of any handler supplied as an argument, for example for the invocation `./lambda-rapid python3 -m awslambdaric echo.handler` then AWS_LAMBDA_FUNCTION_NAME would default to echo. This value is used to register functions with the "router", so it is important to ensure that it correctly corresponds with the name that will be used to invoke the function and also that each function has a unique name.
* AWS_LAMBDA_FUNCTION_VERSION: The version of the function being executed. The default value is $LATEST.
* AWS_LAMBDA_FUNCTION_TIMEOUT: The Lambda function execution timeout. The default value is 3 seconds as per the [AWS Lambda default value](https://docs.aws.amazon.com/lambda/latest/dg/configuration-function-common.html#configuration-timeout-console).
* AWS_LAMBDA_FUNCTION_IDLETIMEOUT: The Lambda function idle timeout. N.B. This value isn't configurable with real AWS Lambda. After a period of inactivity unused/underused Lambda functions will be disconnected and will exit. This also includes instances that were started to service a period of higher concurrency requirements where the required concurrency level has since reduced. The default value is 1800 seconds (30 minutes e.g. twice the maximum AWS Lambda function timeout).

#### Lambda Server
Currently supported environment variables specific to the Lambda Server are:

* LOG_LEVEL: The log level (PANIC FATAL ERROR WARNING INFO DEBUG TRACE). The level is case insensitive and the default value is INFO.
* INVOKE_API_HOST: The Invoke API address, default is 0.0.0.0.
* PORT: The Invoke API listen port, default is 8080.
* AMQP_URI: The connection URI to an AMQP message broker (e.g. RabbitMQ). The URI is of the format `amqp://username:password@host:port/virtual_host?key=value&key=value`, the default value is: `amqp://guest:guest@localhost:5672?connection_attempts=20&retry_delay=10&heartbeat=0`. Note that the default `localhost` address won't behave in the way that may be expected if the Lambda Server is deployed in a container, as localhost will refer to the *container's* localhost, which is unlikely to be where the AMQP broker is bound to. In practice therefore, for container deployments the AMQP_URI environment variable most likely will need to be *explicitly* set with the required broker URI.

In addition the Lambda environment variable AWS_LAMBDA_FUNCTION_TIMEOUT is supported, though its behaviour is different to that of the Daemon as the Lambda Server can't know the timeouts set for each Lambda as they are decoupled by a messaging fabric. In most cases the Lambdas will themselves send a response message when they time out which will simply be returned by the Lambda Server, but we also have a fail safe timeout here for cases where a Lambda fails between receiving a request and sending a response. This timeout needs to be higher than the highest expected legitimate Lambda timeout. We set the default to be 1800 seconds (30 minutes) as that is twice the AWS Lambda max timeout of 15 minutes and also happens to be the RabbitMQ ack timeout where the broker closes a channel if a message acknowledge hasn't occurred within 30 minutes of the message being delivered.

Invocation requests to non-existent or non-deployed Lambdas are identified quickly and are not affected by AWS_LAMBDA_FUNCTION_TIMEOUT as requests are published with Mandatory set true, so if they are unroutable they will be returned by the broker. The purpose of this timeout is to cater for cases where the requests have actually succeeded, but the Lambda fails to respond at all.