# kind-openfaas
[OpenFaas](https://www.openfaas.com/) is an [Open Source](https://github.com/openfaas/faas) FaaS framework with a [Kubernetes provider back-end](https://github.com/openfaas/faas-netes) that enables Kubernetes support for OpenFaaS.

A key part of the way OpenFaaS Functions work is the OpenFaaS [watchdog](https://docs.openfaas.com/architecture/watchdog/), which is responsible for starting and monitoring Functions in OpenFaaS. The watchdog is the PID 1 (init) process for Functions (e.g the ENTRYPOINT for OpenFaaS Function containers) that implements an HTTP server listening on port 8080, and acts as a reverse proxy for running Functions and microservices.

The Lambda Runtime API Daemon acts as PID 1 for Lambda Execution Environments and can be configured to expose the OpenFaaS [watchdog](https://github.com/openfaas/of-watchdog) API simply by setting the environment variable `ENABLE_OPENFAAS` to `true`. This exposes the additional routes `/`, `/_/health`, and `/_/ready` on the Invoke API allowing unmodified Lambda Container Images to be deployed as OpenFaaS Functions.

[kind](https://kind.sigs.k8s.io/) is a tool for running local Kubernetes multi-node clusters using Docker containers as nodes.

This example illustrates standing up a Kubernetes cluster using kind, the cluster has the Kubernetes Dashboard, OpenFaaS, and a simple (insecure) local container registry deployed, so the example is standalone with few additional dependencies. With the cluster deployed the example goes on to illustrate deploying the [echo/echo-lambda](../../echo/echo-lambda) and [wasm/image-greyscale](../../wasm/image-greyscale) example Lambdas as OpenFaas Functions.

## Prerequisites
In addition to Docker this example requires a few CLIs to be installed and available on the user's PATH.

**kind**
https://kind.sigs.k8s.io/docs/user/quick-start#installation
```
curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.20.0/kind-linux-amd64
chmod +x ./kind

# mv to /usr/local/bin or other directory on PATH
sudo mv ./kind /usr/local/bin/kind
```

**kubectl**
https://kubernetes.io/docs/tasks/tools/install-kubectl-linux/

This downloads kubectl v1.27.3 to be consistent with the Kubernetes version deployed with kind v0.20.0. If a different release of kind is deployed the kubectl version may need to be modified to remain consistent.
```
curl -LO https://dl.k8s.io/release/v1.27.3/bin/linux/amd64/kubectl
chmod +x ./kubectl

# mv to /usr/local/bin or other directory on PATH
sudo mv ./kubectl /usr/local/bin/kubectl
```
**helm**
https://helm.sh/docs/intro/install/#from-the-binary-releases

The OpenFaaS Kubernetes provider [faas-netes](https://github.com/openfaas/faas-netes) is [installed with helm](https://github.com/openfaas/faas-netes/tree/master/chart/openfaas#2-install-with-helm).
```
# Helm is released in an archive so need to untar too
curl -sL https://get.helm.sh/helm-v3.12.1-linux-amd64.tar.gz | tar xz linux-amd64/helm --strip-components 1
chmod +x ./helm

# mv to /usr/local/bin or other directory on PATH
sudo mv ./helm /usr/local/bin/helm
```
**faas-cli**
https://docs.openfaas.com/cli/install/
https://github.com/openfaas/faas-cli/releases
```
curl -Lo ./faas-cli https://github.com/openfaas/faas-cli/releases/download/0.16.7/faas-cli
chmod +x ./faas-cli

# mv to /usr/local/bin or other directory on PATH
sudo mv ./faas-cli /usr/local/bin/faas-cli
```

## Deploy Kubernetes and OpenFaaS
The [cluster](cluster) script is used to stand up a Kubernetes cluster named `kind-openfaas` using [kind](https://kind.sigs.k8s.io/). The cluster is deployed with one control plane node and four worker nodes.

The cluster exposes the Kubernetes dashboard on port 30000, a simple (insecure) container registry on port 5000 and the OpenFaaS gateway on port 8080. Those ports are configured in [cluster-config.yaml](cluster-config.yaml) and work by mapping the kind control plane Docker container host ports to container ports that match the Kubernetes nodePort values for the Kubernetes dashboard, OpenFaaS gateway and container registry services.

To start the cluster:
```
./cluster up
```
To stop the cluster:
```
./cluster down
```
**Important Note/Disclaimer**. The cluster script is intended to be simple and intuitive to use at the expense of security, so note that the script will print the token required by the Kubernetes dashboard and the generated OpenFaaS password to stdout.

Once the cluster is available, which may take a few minutes, the Kubernetes dashboard may be accessed by navigating a browser to `https://localhost:30000/` the initial access token should have been printed by the cluster script or a new one may be generated by running:
```
kubectl --context kind-openfaas -n kubernetes-dashboard create token admin-user
```
The OpenFaaS dashboard may be accessed by navigating a browser to `http://localhost:8080` and logging in with the user name `admin` and the OpenFaaS admin password that is printed to stdout during cluster start up. The password may also be retrieved by the command:
```
kubectl --context kind-openfaas -n openfaas get secret basic-auth -o jsonpath="{.data.basic-auth-password}" | base64 --decode
```

## Deploy Lambdas as OpenFaaS Functions
For this example we will deploy the [echo/echo-lambda](../../echo/echo-lambda) and [wasm/image-greyscale](../../wasm/image-greyscale) example Lambdas as OpenFaas Functions. For the following instructions it is a prerequisite that the Lambda Container Images for those examples have been build locally by following the instructions in their respective repositories.

The Lambda Container Images for those examples were built **without** the Lambda Runtime API Daemon being "bundled" in their images, rather we ran those examples by bind-mounting the Daemon to /usr/local/bin/aws-lambda-rie at **run-time**.

Mounting the Daemon in the way illustrated by those examples is ideal during development and also decouples the deployment lifecycle of the Daemon and the Lambdas, though it adds complications for Kubernetes based deployments. Because OpenFaaS "hides" the underlying Kubernetes deployment and service of the functions by using the faas-cli to deploy, for now it is simpler to bundle the Daemon with Lambdas we wish to deploy to OpenFaaS so we create new container images that simply add the Daemon.

[Dockerfile-echo](Dockerfile-echo) extends the echo-lambda image, copying the Runtime API Daemon and naming it aws-lambda-rie, as the ENTRYPOINT is set to use /usr/local/bin/aws-lambda-rie to be compatible with AWS images.
```
FROM echo-lambda

COPY lambda-runtime-api-daemon /usr/local/bin/aws-lambda-rie
```
Similarly, [Dockerfile-image-greyscale](Dockerfile-image-greyscale) extends image-greyscale-lambda.
```
FROM image-greyscale-lambda

COPY lambda-runtime-api-daemon /usr/local/bin/aws-lambda-rie
```

To build the images locally:
```
cp ${PWD}/../../../bin/lambda-runtime-api-daemon .
docker build -t localhost:5000/echo-lambda-rapid -f Dockerfile-echo .
docker build -t localhost:5000/image-greyscale-lambda-rapid -f Dockerfile-image-greyscale .
rm lambda-runtime-api-daemon
```
This copies the `lambda-runtime-api-daemon` executable so it is visible to the Docker context and sets a `localhost:5000/` prefix on the tags as we shall be pushing the images to our Kubernetes hosted local container registry.

To push the images to the local container registry:
```
docker push localhost:5000/echo-lambda-rapid
docker push localhost:5000/image-greyscale-lambda-rapid
```
To check that the images have been successfuly deployed to the local container registry:
```
curl http://localhost:5000/v2/_catalog
```
which should return:
```
{"repositories":["echo-lambda-rapid","image-greyscale-lambda-rapid"]}
```
Finally, to deploy the Lambdas to OpenFaaS:
```
export OPENFAAS_URL=http://localhost:8080
faas-cli deploy -f ./openfaas-function-stack.yaml
```
To check that the Lambdas have been deployed as OpenFaaS Functions:
```
faas-cli list
```
which should return:
```
Function                      	Invocations    	Replicas
echo-lambda                   	0              	1
image-greyscale-lambda        	0              	1
```
or navigate a browser to `http://localhost:8080` to log in to the OpenFaaS dashboard.

The [openfaas-function-stack.yaml](openfaas-function-stack.yaml) used by the `faas-cli deploy` command is a fairly simple OpenFaaS Function [deployment stack](https://docs.openfaas.com/reference/yaml/):
```
provider:
  name: openfaas
functions:
  echo-lambda:
    image: localhost:5000/echo-lambda-rapid:latest
    lang: dockerfile
    environment:
      ENABLE_OPENFAAS: true
  image-greyscale-lambda:
    image: localhost:5000/image-greyscale-lambda-rapid:latest
    lang: dockerfile
    environment:
      ENABLE_OPENFAAS: true
```
The most notable part of this configuration is setting the `ENABLE_OPENFAAS` environment variable to `true`, which configures the Lambda Runtime API Daemon to expose the invoke and health check routes needed for the Daemon to emulate the OpenFaaS watchdog.
## Usage
### echo-lambda
**To invoke the Lambda via curl:**
```
curl -XPOST "http://localhost:8080/function/echo-lambda" -d '{"key": "value"}'
```
This simply POSTs JSON request data to the OpenFaaS [HTTP / webhooks](https://docs.openfaas.com/reference/triggers/#http-webhooks) build-in trigger.

### image-greyscale-lambda
This Lambda expects requests and responses to be formatted using the AWS API Gateway [Lambda proxy integration syntax](https://docs.aws.amazon.com/apigateway/latest/developerguide/set-up-lambda-proxy-integrations.html#api-gateway-simple-proxy-for-lambda-output-format) e.g.
```
{
  "isBase64Encoded": true|false,
  "statusCode": httpStatusCode,
  "headers": { "headerName": "headerValue", ... },
  "multiValueHeaders": { "headerName": ["headerValue", "headerValue2", ...], ... },
  "body": "..."
}
```
and the request and response `body` field is expected to be hex encoded.

**To invoke the Lambda via curl:**
First we create the request by reading and converting the image to hex in a subshell.

We create a `request.txt` file because curl errors with "Argument list too long" if we attempt to directly use its `-d` option with large items, so we instead do `-d @request.txt`.

We use [xxd](https://linux.die.net/man/1/xxd) to convert to and from hex encoding as follows:
```
xxd -p | tr -d '\n'
```
converts stdin to hex and removes the newlines that xxd includes
```
xxd -r -p
```
converts stdin from hex to binary.

So the following creates a JSON request for the Lambda, with a body that is a hex encoded version of the `savannah_cat.jpg` image
```
echo '{"body": "'$(cat savannah_cat.jpg | xxd -p | tr -d '\n')'"}' > request.txt
```
Given a request.txt we can invoke the Lambda via the OpenFaaS [HTTP / webhooks](https://docs.openfaas.com/reference/triggers/#http-webhooks) build-in trigger.
```
curl -XPOST "http://localhost:8080/function/image-greyscale-lambda" -d @request.txt | grep -Po '"'"body"'"\s*:\s*"\K([^"]*)' | xxd -r -p > output.png
```
This example pipes the response via grep to extract the body field using a [grep regex found on stackoverflow](https://stackoverflow.com/questions/1955505/parsing-json-with-unix-tools#comment42500081_1955505). It may be cleaner to use `jq` to extract the body, but most Linux distros don't have that installed by default whereas grep is ubiquitous.

If all is well this should write a greyscale version of the original image as `output.png`.