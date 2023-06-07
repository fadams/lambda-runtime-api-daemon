//
// Licensed to the Apache Software Foundation (ASF) under one
// or more contributor license agreements.  See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership.  The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.
//

// This is a simple C application intended to be used as the ENTRYPOINT for
// Lambda Container Images. It first tests that there is a single argument,
// which represents the Lambda handler (which for go Lambdas is a binary).
// The application next tests the AWS_LAMBDA_RUNTIME_API environment variable,
// which if set means the image is running in AWS Lambda so we directly
// exec the handler and if AWS_LAMBDA_RUNTIME_API is not set we start
// rapid or rie. Note that rapid is mounted to /usr/local/bin/aws-lambda-rie
// as a convention to make it easier to switch between rie and rapid and use
// AWS base images more seamlessly.

// Build with glibc
// gcc -Os -s -static entrypoint.c -o entrypoint
//
// Build with musl (much smaller executable than with glibc)
// apt install musl-tools
// musl-gcc -Os -s -static entrypoint.c -o entrypoint

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
