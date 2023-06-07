# echo-lambda-go
This is a Lambda Container Image for a go based Lambda providing a simple echo service. This example builds a custom Lambda Container Image.

The [Dockerfile](Dockerfile) is somewhat more complicated than the equivalent [Python based Lambda Container Image](../echo-lambda), largely because we actually need to compile go applications.

The main [Dockerfile](Dockerfile) in this repository is a two stage Dockerfile. The first stage builds the main artefacts:
```
FROM golang:1.18 as builder
WORKDIR /go/src/echo-lambda
COPY . .
RUN apt-get update && DEBIAN_FRONTEND=noninteractive \
  apt-get install -y --no-install-recommends \
  musl-tools && \
  make dep && make entrypoint && make echo
```
and the second stage copies those to a scratch image.
```
FROM scratch
COPY --from=builder /go/src/echo-lambda/echo /echo
COPY --from=builder /go/src/echo-lambda/entrypoint /entrypoint

ENTRYPOINT ["/entrypoint"]
CMD ["/echo"]
```
The approach taken with this image is a little different from the approach taken in the AWS documentation for go Lambdas, in particular it makes use of the fact that it is trivial to compile go applications to static binaries thus making it simple to use [scratch Docker images](https://hub.docker.com/_/scratch/).

The [Makefile](Makefile) does all the work to compile the go application to a static binary:
```
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags lambda.norpc -ldflags "-s -w" -o ./ echo
```
Setting `CGO_ENABLED=0` allows creation of a statically linked executable and `ldflags -s -w` disables [DWARF](https://dwarfstd.org/) and symbol table generation to reduce binary size. The `lambda.norpc` tag drops the (legacy) go Lambda RPC mode dependencies, which results in a smaller binary size and user space surface. Legacy RPC mode is not required as we are exclusively using the Lambda Runtime API.

Note that because it is a compiled language AWS does not provide a separate Runtime Interface Client for go like Python's awslambdaric. Instead, the [go package](https://github.com/aws/aws-lambda-go) `aws-lambda-go/lambda` provides an implementation of the runtime interface, which may be used by a go application to launch the Lambda handler e.g.:
```
package main

import (
  "context"
  "github.com/aws/aws-lambda-go/lambda"
)

func EchoHandler(ctx context.Context, event any) (any, error) {
  return event, nil
}

func main() {
  lambda.Start(EchoHandler)
}
```

*In theory* a statically compiled go Lambda application binary is all that we require. However, as with our other examples, to make it simpler to either directly use the executable when the image is deployed to the AWS Lambda Service or to run via the Lambda Runtime API Daemon we supply a simple container ENTRYPOINT.

The most obvious approach might be to use a simple [lambda-entrypoint.sh](lambda-entrypoint.sh) script as we have used in the other examples and as illustrated in the AWS documentation:
```
#!/bin/sh
if [ $# -ne 1 ]; then
  echo "entrypoint requires the handler name to be the first argument" 1>&2
  exit 142
fi

if [ -z "${AWS_LAMBDA_RUNTIME_API}" ]; then
  exec /usr/local/bin/aws-lambda-rie "$@"
else
  exec "$@"
fi
```
Unfortunately, however, this won't work in a scratch image as there is no shell, so instead we use a simple C application [entrypoint.c](lambda-entrypoint/entrypoint.c) that does the same job:
```
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

int main(int argc, char *argv[]) {
  // Check that a handler has been supplied, which will be the first argument.
  // We shouldn't need additional arguments, so we explicitly check that argc
  // is two (arg 0 is this executable and arg 1 is the handler).
  if (argc != 2) {
      printf("entrypoint requires the handler name to be the first argument\n");
      return 142;
  } else {
      char* cmd = argv[1];
      if (getenv("AWS_LAMBDA_RUNTIME_API")) { // AWS_LAMBDA_RUNTIME_API is set
      execlp(cmd, cmd, (char*)0);
    } else { // AWS_LAMBDA_RUNTIME_API is not set
      cmd = "/usr/local/bin/aws-lambda-rie";
      execlp(cmd, cmd, argv[1], (char*)0);
    }

    // If execlp() is successful, we should not reach here.
    printf("exec: %s: not found\n", cmd);
    return 127;
  }
}
```
We use [musl-gcc](https://www.musl-libc.org/how.html) to compile entrypoint.c:
```
musl-gcc -Os -s -static entrypoint.c -o entrypoint
```
where `-Os` optimises for size, `-s` strips the binary, and `-static` creates a static executable linked with musl libc, which creates a small static binary of around 31K in size to use as the ENTRYPOINT.

The final Container Image built off scratch simply copies the `entrypoint` and `echo` executables and sets ENTRYPOINT and CMD to uses these, giving an overall image size of 4.8MB.

To build the image:
```
docker build -t echo-lambda-go .
```
Although building scratch images for languages that can be statically compiled offers many advantages in terms of reduced image size and user space surface it is important to be mindful of potential [pitfalls](https://iximiuz.com/en/posts/containers-distroless-images/#scratch-containers-pitfalls). In particular some commonly required files are missing:

- /etc/passwd and /etc/group entries for a user and group information
- ca-certificates e.g. /etc/ssl/certs/ca-certificates.crt
- tzdata e.g. /usr/share/zoneinfo
- /tmp

For this trivial echo Lambda there is no issue, but there may be for other application. The simplest way to resolve any issues is to use a regular base image like `ubuntu:20.04`, but this will increase the image size somewhat. An Alpine base image should also work and will be smaller than an Ubuntu base.

A better approach might be a [distroless](https://github.com/GoogleContainerTools/distroless/tree/main/base) image like `gcr.io/distroless/static` which is intended for this scenario. Alternatively, updating the second stage of the Dockerfile to copy the required files from the first stage offers more control to add only what is actually required by the application e.g.:
```
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /tmp /tmp
```
Additionally, it is often better to use tmpfs which may be achieved in Docker by including the following in the `docker run` command:
```
--mount 'type=tmpfs,dst=/tmp,tmpfs-mode=1777'
```
As previously mentioned, this approach of building go Lambdas as static binaries in scratch images differs quite significantly from what is covered in the [AWS documentation](https://docs.aws.amazon.com/lambda/latest/dg/go-image.html), so this repository also contains two alternative Dockerfiles to more directly illustrate those examples.

[Dockerfile-awsgobase](Dockerfile-awsgobase) follow the approach illustrated in the AWS [go:1 base image documentation](https://docs.aws.amazon.com/lambda/latest/dg/go-image.html#go-image-v1) and may be build with:
```
docker build -t echo-lambda-go -f Dockerfile-awsgobase .
```
[Dockerfile-awsprovidedbase](Dockerfile-awsprovidedbase) follow the approach illustrated in the AWS [provided:al2 base image documentation](https://docs.aws.amazon.com/lambda/latest/dg/go-image.html#go-image-al2) and may be build with:
```
docker build -t echo-lambda-go -f Dockerfile-awsprovidedbase .
```
Both of these images are *significantly* larger than the scratch image and offer no real advantages so they are provided here primarily for completeness.

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
  echo-lambda-go

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

First use `make dep && make` to build  the `echo` Lambda locally (this requires go to be installed locally) then from the echo-lambda-go directory running:
```
../../../bin/lambda-runtime-api-daemon ./echo
```

will launch `lambda-runtime-api-daemon` using the relative path to the executable.

This way of running `lambda-runtime-api-daemon` can be useful during development as it avoids the need to build Container Images.