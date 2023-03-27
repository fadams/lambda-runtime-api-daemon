#
# Licensed to the Apache Software Foundation (ASF) under one
# or more contributor license agreements.  See the NOTICE file
# distributed with this work for additional information
# regarding copyright ownership.  The ASF licenses this file
# to you under the Apache License, Version 2.0 (the
# "License"); you may not use this file except in compliance
# with the License.  You may obtain a copy of the License at
# 
#   http://www.apache.org/licenses/LICENSE-2.0
# 
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.
#

# A more sophisticated Lambda using custom Lambda container image.

# This Lambda classifies images using WasmEdge Tensorflow to run AI inference,
# but the more interesting aspect is that the actual inference is
# done by a Rust application that is compiled to WebAssembly - more
# precisely WebAssembly System Interface (WASI)
# https://hacks.mozilla.org/2019/03/standardizing-wasi-a-webassembly-system-interface/
# https://github.com/bytecodealliance/wasmtime/blob/main/docs/WASI-intro.md
# The .wasm bytecode is then itself Ahead Of Time (AOT) compiled
# for performance and at run time is executed by the WasmEdge runtime
# in a separate process with the Lambda request body passed to WasmEdge
# stdin and the image classification returned on WasmEdge stdout, captured
# by the Lambda and returned.

# This Lambda is based on the WasmEdge https://wasmedge.org example:
# https://wasmedge.org/book/en/use_cases/frameworks/serverless/aws.html#example-2-ai-inference
#
# Model info:
# The model used is the Mobilenet_V1_1.0_224_quant described in
# https://www.tensorflow.org/lite/examples/image_classification/overview
# https://storage.googleapis.com/download.tensorflow.org/models/tflite/mobilenet_v1_1.0_224_quant_and_labels.zip
#
#    224x224 image classification, Quantized models, Model size (4.3 Mb)
#    Download the quantized modelfrom Here
#    Paper - https://arxiv.org/pdf/1712.05877.pdf
#

# First stage compiles the Rust application to WebAssembly
FROM ubuntu:20.04 AS builder

ENV RUSTUP_HOME=/usr/local/bin/rustup \
    CARGO_HOME=/usr/local/bin/cargo \
    PATH=/usr/local/bin/cargo/bin:$PATH

COPY image-classification-rust /usr/local/src/image-classification-rust

# cargo build writes the compiled WebAssembly bytecode to:
# /usr/local/src/image-classification-rust/target/wasm32-wasi/release/image-classification.wasm

RUN apt-get update && DEBIAN_FRONTEND=noninteractive \
    apt-get install -y --no-install-recommends \
    curl ca-certificates build-essential \
    # Needed by reqwest crate
    pkg-config libssl-dev && \
    # From https://www.rust-lang.org/tools/install, but
    # passing args rather than interactive install.
    # Add musl target to build static executables.
    # Add wasm32-wasi target to build WebAssembly executables.
    curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | \
    sh -s -- -y --no-modify-path \
    --target x86_64-unknown-linux-musl --target wasm32-wasi && \
    chmod -R a+w $RUSTUP_HOME $CARGO_HOME && \
    # Compile the Rust image classification application to WebAssembly
    # The compiled WebAssembly target is at the following path.
    # target/wasm32-wasi/release/image-classification.wasm
    cd /usr/local/src/image-classification-rust && \
    cargo build --release --target wasm32-wasi && \
    # Internet resources often show the following WRT Dockerfile pruning:
    # rm -rf /var/lib/{apt,dpkg,cache,log}
    # but that doesn't *actually* work in Dockerfiles, as it relies on bash
    # brace expansion, whereas Dockerfile RUN uses /bin/sh by default.
    # In addition removing /var/lib/dpkg actually breaks apt in child images.
    # The following does what that rm is _supposed_ to do:
    cd /var/lib && rm -rf apt cache log


# Second stage builds the Node.js aws-lambda-ric then the Lambda app
FROM ubuntu:20.04

# Minimise unnecessary dependencies to keep image size down
ENV NODE_ENV=production

ENV LAMBDA_TASK_ROOT=/usr/local/lib
# See also https://docs.aws.amazon.com/lambda/latest/dg/configuration-envvars.html#configuration-envvars-runtime

# (Optional) Add Lambda Runtime Interface Emulator
#ADD https://github.com/aws/aws-lambda-runtime-interface-emulator/releases/latest/download/aws-lambda-rie /usr/local/bin/aws-lambda-rie

# Use a script in the ENTRYPOINT for simpler local runs
COPY lambda-entrypoint.sh /

# Copy function code
COPY image-classification.js ${LAMBDA_TASK_ROOT}/

# Copy compiled WebAssembly bytecode from Rust builder image
COPY --from=builder /usr/local/src/image-classification-rust/target/wasm32-wasi/release/image-classification.wasm ${LAMBDA_TASK_ROOT}/

RUN apt-get update && DEBIAN_FRONTEND=noninteractive \
    apt-get install -y --no-install-recommends \
    # Needed to install Node.js binary archive
    ca-certificates curl \
    # Needed by npm to install aws-lambda-ric, as it contains
    # c++ code which needs to be compiled.
    make cmake autoconf automake libtool \
    libcurl4-openssl-dev g++ python3 && \
    # --------------------------------------------------------------------------
    # Install LTS Node.js via binary archive
    # https://github.com/nodejs/help/wiki/Installation
    VERSION=v18.15.0 ARCH=linux-x64 DISTRO=node-${VERSION}-${ARCH} && \
    curl -sSL https://nodejs.org/dist/${VERSION}/${DISTRO}.tar.gz | \
    tar -xzv -C /usr/local/bin/ && \
    # Install node-prune https://github.com/tj/node-prune
    curl -sf https://gobinaries.com/tj/node-prune | sh && \
    # symlink the Node.js binaries to a sane location
    ln -s /usr/local/bin/${DISTRO}/bin/node /usr/local/bin/node && \
    ln -s /usr/local/bin/${DISTRO}/bin/npm /usr/local/bin/npm && \
    # Build and install Lambda Runtime Interface Client for Node.js via npm
    cd ${LAMBDA_TASK_ROOT} && \
    npm install aws-lambda-ric && \
    # Comment out a slightly pointless log message to make things
    # closer to how the Python aws-lambda-ric behaves at startup.
    sed -i 's/console.log/\/\/console.log/' \
    ${LAMBDA_TASK_ROOT}/node_modules/aws-lambda-ric/bin/index.js && \
    # Prune node_modules
    node-prune /usr/local/lib && \
    # Remove node-prune after we've finished with it
    rm -rf /usr/local/bin/node-prune && \
    # After installing aws-lambda-ric via npm remove deps path
    rm -rf /usr/local/lib/node_modules/aws-lambda-ric/deps && \
    rm -rf /usr/local/lib/node_modules/node-gyp && \
    # After installing packages via npm remove various node paths
    # (including npm) not needed at run time to reduce image size.
    rm -rf /usr/local/bin/${DISTRO}/include && \
    rm -rf /usr/local/bin/${DISTRO}/lib && \
    rm -rf /usr/local/bin/${DISTRO}/share && \
    rm -rf /usr/local/bin/${DISTRO}/CHANGELOG.md && \
    rm -rf /usr/local/bin/${DISTRO}/README.md && \
    rm -rf /usr/local/bin/${DISTRO}/bin/npm && \
    rm -rf /usr/local/bin/npm && \
    # --------------------------------------------------------------------------
    # Install wasmedge & wasmedgec with tensorflow tools
    VERSION=0.8.2 DEPS_VERSION=0.8.2 && \
    # https://github.com/second-state/WasmEdge-tensorflow-tools
    curl -sSL https://github.com/second-state/WasmEdge-tensorflow-tools/releases/download/${VERSION}/WasmEdge-tensorflow-tools-${VERSION}-manylinux2014_x86_64.tar.gz | \
    tar -xzv wasmedge-tensorflow-lite wasmedgec-tensorflow && \
    # https://github.com/second-state/WasmEdge-tensorflow-deps
    curl -sSL https://github.com/second-state/WasmEdge-tensorflow-deps/releases/download/${DEPS_VERSION}/WasmEdge-tensorflow-deps-TFLite-${DEPS_VERSION}-manylinux2014_x86_64.tar.gz | \
    tar -xzv && \
    # AOT Compile WebAssembly bytecode to .so for performance
    ./wasmedgec-tensorflow --generic-binary \
    image-classification.wasm image-classification.so && \
    # Remove wasmedgec to reduce image size, as it's no longer required
    rm wasmedgec-tensorflow && \
    # --------------------------------------------------------------------------
    chmod -R 755 /usr/local/bin && \
    chmod 755 /lambda-entrypoint.sh && \
    apt-get clean && \
    # Remove packages only used to install Node.js and build aws-lambda-ric
    apt-get purge -y curl make cmake autoconf automake libtool \
    g++ python3 && \
    apt-get autoremove -y && \
    # Clean up npm/apt/dpkg cache & logs
    rm -rf /root/.npm /root/.cache && \
    # Internet resources often show the following WRT Dockerfile pruning:
    # rm -rf /var/lib/{apt,dpkg,cache,log}
    # but that doesn't *actually* work in Dockerfiles, as it relies on bash
    # brace expansion, whereas Dockerfile RUN uses /bin/sh by default.
    # In addition removing /var/lib/dpkg actually breaks apt in child images.
    # The following does what that rm is _supposed_ to do:
    cd /var/lib && rm -rf apt cache log

# The aws-lambda-ric is launched by npx, which tries to write
# logs to /.npm/_logs Quite apart from the fact that we will
# be running as a non-root user Lambda runs with a readonly
# root filesystem, so we set npm_config_cache to /tmp.
ENV npm_config_cache=/tmp

# Set working directory to function root directory
WORKDIR ${LAMBDA_TASK_ROOT}

ENTRYPOINT ["/lambda-entrypoint.sh"] 

# Set the CMD to handler
CMD ["image-classification.handler"]

#-------------------------------------------------------------------------------
# docker build -t image-classification-lambda .
#

# This Lambda expects input/output in API Gateway syntax e.g. body in the
# "body" field. The request and response body are hex encoded.

# To invoke the Lambda via curl:
# First create request by reading and converting the image to hex in subshell
# We create a request.txt file because curl errors with Argument list too long
# if we attempt to directly use -d with large items.
# We use xxd to convert to and from hex encoding:
#
# xxd -p | tr -d '\n'
# converts stdin to hex and removes the newlines that xxd includes
#
# xxd -r -p
# converts stdin from hex to binary
#
# echo '{"body": "'$(cat savannah_cat.jpg | xxd -p | tr -d '\n')'"}' > request.txt
# echo '{"body": "'$(cat pizza.jpg | xxd -p | tr -d '\n')'"}' > request.txt
# echo '{"body": "'$(cat Leopard2.jpg | xxd -p | tr -d '\n')'"}' > request.txt
# echo '{"body": "'$(cat Mandarin.jpg | xxd -p | tr -d '\n')'"}' > request.txt
# echo '{"body": "'$(cat Burger.jpg | xxd -p | tr -d '\n')'"}' > request.txt
# echo '{"body": "'$(cat Agaric.jpg | xxd -p | tr -d '\n')'"}' > request.txt
#
# Given a request.txt we invoke the Lambda with generic name:
#
# curl -XPOST "http://localhost:8080/2015-03-31/functions/function/invocations" -d @request.txt
# or with its proper name (this won't work with RIE)
#
# curl -XPOST "http://localhost:8080/2015-03-31/functions/image-classification-lambda/invocations" -d @request.txt
#

