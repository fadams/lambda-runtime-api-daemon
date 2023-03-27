# examples
This examples directory provides some simple *quick start* style examples and is a good place to become acquainted with the Lambda Runtime API Daemon.

* [echo](echo): Illustrates a range of different ways to build Lambda Container images for a simple echo service along with clients illustrating different ways to invoke the echo Lambda.

* [wasm](wasm): This is a good deal more interesting than the simple echo examples and contains examples of Lambdas where the actual processing is implemented by applications written in Rust and compiled to WebAssembly (WASM) then executed from a Node.js based Lambda using the WasmEdge WebAssembly runtime for performance and security.

* [utils](utils): This directory isn't an example, rather it contains some utility code used by the some of the Python client examples.