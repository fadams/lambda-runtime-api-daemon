# examples
This examples directory provides some simple *quick start* style examples and is a good place to become acquainted with the Lambda Runtime API Daemon.

* [echo](echo): Illustrates a range of different ways to build Lambda Container images for a simple echo service along with clients illustrating different ways to invoke the echo Lambda.

* [extensions](extensions): Provides examples illustrating how to build and use Lambda Internal and External Extensions.

* [wasm](wasm): This is a good deal more interesting than the simple echo examples and contains examples of Lambdas where the actual processing is implemented by applications written in Rust and compiled to WebAssembly (WASM), then executed from a Node.js based Lambda using the WasmEdge WebAssembly runtime for performance and security.

* [kubernetes/kind-lambda](kubernetes/kind-lambda): Illustrates standing up a local Kubernetes cluster with [kind](https://kind.sigs.k8s.io/) then deploying the Lambda Server and example Lambda Container Images to the cluster, using init Containers and Volumes to mount the lambda-runtime-api-daemon executable so we can use unmodified Lambda Container Images. This example also illustrates a range of different ways to scale the Lambdas using Kubernetes [Horizontal Pod Autoscaling](https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/) or the Runtime API Daemon's own MAX_CONCURRENCY setting.

* [kubernetes/kind-openfaas](kubernetes/kind-openfaas): Illustrates standing up a local Kubernetes cluster with [kind](https://kind.sigs.k8s.io/) then deploying [OpenFaaS](https://www.openfaas.com/) to it and deploying Lambda Container Images bundled with the Runtime API Daemon configured to act as an OpenFaaS Function watchdog.

* [utils](utils): This directory isn't an example, rather it contains some utility code used by the some of the Python client examples.