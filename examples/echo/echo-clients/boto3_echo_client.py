#!/usr/bin/env python3
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
# Run with:
# python3 boto3_echo_client.py
#

"""
This example uses boto3 to invoke a Lambda and retrieve the response.
It is intended to be more illustrative than production ready, but shows
both basic invocation in a loop and also using a ThreadPoolExecutor
noting that using the ThreadPoolExecutor approach may actually *reduce*
throughput for highly IO bound cases such as for our simple echo Lambda.
For cases where the Lambda does non-trivial work the additional
concurrency that the ThreadPoolExecutor buys is likely worthwhile,
though the asyncio/aioboto3 based approach is likely far more efficient.
"""

import sys
assert sys.version_info >= (3, 0) # Bomb out if not running Python3

import base64, botocore, boto3, time, uuid
from botocore.exceptions import ClientError

import concurrent.futures

"""
Attempt to use ujson if available https://pypi.org/project/ujson/
"""
try:
    import ujson as json
except:  # Fall back to standard library json
    import json

# Use of Lambda ClientContext isn't well documented and quite fiddly so we
# include an example here. The most useful "user" bit is the custom field
# https://docs.aws.amazon.com/lambda/latest/dg/python-context.html
def serialise_lambda_client_context(custom=None, env=None, client=None):
    context = {"custom": custom, "env": env, "client": client}
    json_context = json.dumps(context).encode('utf-8') # Get JSON as bytes
    b64_context = base64.b64encode(json_context).decode('utf-8')
    return b64_context

context = {
    "custom": {"foo": "bar"},
    "env": {"test": "test"},
    "client": {}
}

# From boto3 example illustrating increasing max_pool_connections and
# calling via concurrent.futures.ThreadPoolExecutor
# https://github.com/boto/boto3/issues/2567
#MAX_WORKERS = 100
#MAX_WORKERS = 20
MAX_WORKERS = 10

# Maximum size of connection pool
#MAX_CONNECTIONS = 100
MAX_CONNECTIONS = 10

class EchoClient(object):
    #def __init__(self, function_name, iterations=1000000):
    #def __init__(self, function_name, iterations=100000):
    #def __init__(self, function_name, iterations=10000):
    def __init__(self, function_name, iterations=10):
    #def __init__(self, function_name, iterations=1):
        self.function_name = function_name  # function_name is the Lambda's name
        self.iterations = iterations

        # Initialise the boto3 client setting the endpoint_url to our local
        # Lambda Engine
        # https://boto3.amazonaws.com/v1/documentation/api/latest/reference/core/session.html#boto3.session.Session.client
        self.aws_lambda = boto3.client("lambda", endpoint_url="http://localhost:8080",
            config=botocore.config.Config(max_pool_connections=MAX_CONNECTIONS))

    def invoke_lambda(self, body):
        try:
            response = self.aws_lambda.invoke(
                FunctionName=self.function_name,
                Payload=body,
                #ClientContext=serialise_lambda_client_context(**context),
            )

            #print(response)
            # Read actual object from StreamingBody response.
            # https://botocore.amazonaws.com/v1/documentation/api/latest/reference/response.html#botocore.response.StreamingBody
            response_value = response["Payload"].read().decode("utf-8")
            #response_value = response["Payload"].read()

            return response_value
        except ClientError as e:
            print(e)

    def start_simple_loop(self):
        """
        Setting json_payload to None creates a unique (if trivial) payload
        each iteration otherwise we send the same payload each message.
        It's not very representative, but the main aim is to go fast to test
        processor/Lambda performance.
        """
        #payload = {"payload": "Hello World"}
        payload = {"Hello": str(uuid.uuid4())}
        json_payload = None
        #json_payload = json.dumps(payload)
        #json_payload = '"Hello World"'
        #json_payload = '{"payload": "Hello World"}'
        #json_payload = json.dumps("x" * 50000)

        start = time.time()

        for i in range(self.iterations):
            body = json_payload if json_payload != None else json.dumps({"Hello": str(uuid.uuid4())})
            #print(body)
            results = self.invoke_lambda(body)
            print(results)

        end = time.time()
        print(self.iterations/(end - start))

    def start_executor_loop(self):
        """
        Setting json_payload to None creates a unique (if trivial) payload
        each iteration otherwise we send the same payload each message.
        It's not very representative, but the main aim is to go fast to test
        processor/Lambda performance.
        """
        #payload = {"payload": "Hello World"}
        payload = {"Hello": str(uuid.uuid4())}
        json_payload = None
        #json_payload = json.dumps(payload)
        #json_payload = '"Hello World"'
        #json_payload = '{"payload": "Hello World"}'
        #json_payload = json.dumps("x" * 50000)

        start = time.time()

        with concurrent.futures.ThreadPoolExecutor(MAX_WORKERS) as executor:
            for i in range(0, self.iterations, MAX_WORKERS):
                remaining = self.iterations - i
                task_size = MAX_WORKERS if remaining > MAX_WORKERS else remaining
                futs = []
                for j in range(task_size):
                    body = json_payload if json_payload != None else json.dumps({"Hello": str(uuid.uuid4())})
                    #print(body)
                    futs.append(
                        executor.submit(self.invoke_lambda, body)
                    )
                results = [fut.result() for fut in futs]
                #print(results)

        end = time.time()
        print(self.iterations/(end - start))

if __name__ == '__main__':
    client = EchoClient(function_name="echo-lambda")
    client.start_simple_loop()
    #client.start_executor_loop()

