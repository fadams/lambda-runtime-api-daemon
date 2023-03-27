# echo
This directory contains examples that illustrate a range of different ways to build a simple echo Lambda Container Image along with a range of clients illustrating some of the different ways to invoke the echo Lambda.

* [echo-clients](echo-clients): A range of clients illustrating some of the different ways to invoke the echo Lambda.

* [echo-lambda](echo-lambda): A Lambda Container Image for a Python based Lambda providing a simple echo service. This example builds a custom Lambda Container Image.

* [echo-lambda-aws-base](echo-lambda-aws-base): A Lambda Container Image for a Python based Lambda providing a simple echo service. This example uses the AWS supplied Python Lambda base image.

* [echo-lambda-custom-runtime](echo-lambda-custom-runtime): A Lambda Container Image using a [custom Lambda Runtime](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-custom.html) based on the [Custom Runtime Tutorial](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-walkthrough.html) from the AWS documentation that illustrates creating a Lambda using bash and curl.

* [echo-lambda-nodejs](echo-lambda-nodejs): A Lambda Container Image for a Node.js based Lambda providing a simple echo service. This example builds a custom Lambda Container Image.

* [echo-lambda-nodejs-aws-base](echo-lambda-nodejs-aws-base): A Lambda Container Image for a Node.js based Lambda providing a simple echo service. This example uses the AWS supplied Node.js Lambda base image.