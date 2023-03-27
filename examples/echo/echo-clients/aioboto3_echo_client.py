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
# python3 aioboto3_echo_client.py
#

"""
This example uses aioboto3 to invoke a Lambda and retrieve the response.
It is intended to be more illustrative than production ready, but shows
one approach for launching multiple Lambda invokes as asyncio tasks
concurently using asyncio.gather.
"""

import sys
assert sys.version_info >= (3, 0) # Bomb out if not running Python3

import asyncio, aiobotocore, aioboto3, atexit, base64, contextlib, time, uuid
from botocore.exceptions import ClientError

"""
Attempt to use ujson if available https://pypi.org/project/ujson/
"""
try:
    import ujson as json
except:  # Fall back to standard library json
    import json

"""
Attempt to use uvloop libuv based event loop if available
https://github.com/MagicStack/uvloop
We do this early so calls to asyncio.get_event_loop() get the right one.
"""
try:
    import uvloop
    uvloop.install()
except:  # Fall back to standard library asyncio epoll event loop
    pass

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

# Number of concurrent invocation Tasks
#MAX_WORKERS = 1000
#MAX_WORKERS = 500
#MAX_WORKERS = 100
MAX_WORKERS = 20

# Maximum size of connection pool
#MAX_CONNECTIONS = 100
MAX_CONNECTIONS = 20

class EchoClient(object):
    #def __init__(self, function_name, iterations=1000000):
    #def __init__(self, function_name, iterations=100000):
    #def __init__(self, function_name, iterations=10000):
    def __init__(self, function_name, iterations=10):
    #def __init__(self, function_name, iterations=1):
        self.function_name = function_name  # function_name is the Lambda's name
        self.iterations = iterations

        """
        Initialise the aioboto3 client setting the endpoint_url to our local
        Lambda Engine
        https://boto3.amazonaws.com/v1/documentation/api/latest/reference/core/session.html#boto3.session.Session.client

        The code to create and destroy aioboto3 clients is a little bit "fiddly"
        because since aioboto3 v8.0.0+, client and resource are now async
        context managers. In many cases that can be useful, but creating clients
        is expensive so often we want to create them once at initialisation
        and then use the stored client object for subsequent invocations.
        For that, quite common, use case the use of a context manage is a PITA.

        The way to deal with this is to create an AsyncExitStack, which
        essentially does async with on the context manager returned by client or
        resource, and saves the exit coroutine so that it can be called later to
        clean up.
        https://aioboto3.readthedocs.io/en/latest/usage.html#aiohttp-server-example
        """

        context_stack = contextlib.AsyncExitStack()

        """
        Creating the aioboto3 Lambda Client needs to be async so wrap the logic
        in a nested coroutine and run it using loop.run_until_complete()
        """
        async def create_lambda_client():
            session = aioboto3.Session()
            config = aiobotocore.config.AioConfig(max_pool_connections=MAX_CONNECTIONS)
            aws_lambda = await context_stack.enter_async_context(
                session.client("lambda", endpoint_url="http://localhost:8080",
                               config=config)
            )
            return aws_lambda

        loop = asyncio.get_event_loop()
        self.aws_lambda = loop.run_until_complete(create_lambda_client())

        """
        Tidy up aioboto3 client on exit. This is made slightly awkward as we
        need an async exit handler to await the AsyncExitStack aclose()
        """
        async def async_exit_handler():
            await context_stack.aclose()  # After this the lambda instance should be closed

        """
        Note: exit_handler is a nested closure which "decouples" the lifetime
        of the exit_handler from the lifetimes of instances of the class.
        https://stackoverflow.com/questions/16333054/what-are-the-implications-of-registering-an-instance-method-with-atexit-in-pytho
        """
        @atexit.register
        def exit_handler():
            print("exit_handler called")
            # Run async_exit_handler
            loop.run_until_complete(async_exit_handler())
            print("exit_handler exiting")


    async def invoke_lambda(self, body):
        try:
            response = await self.aws_lambda.invoke(
                FunctionName=self.function_name,
                Payload=body,
                #ClientContext=serialise_lambda_client_context(**context),
            )

            #print(response)
            """
            Read the actual response payload from StreamingBody response.
            https://botocore.amazonaws.com/v1/documentation/api/latest/reference/response.html#botocore.response.StreamingBody
            Note that aioboto3 wraps stream in a context manager:
            https://aioboto3.readthedocs.io/en/latest/usage.html#streaming-download
            so we can't just get the the response using the obvious idiom:
            response_value = response["Payload"].read().decode("utf-8")
            """
            async with response["Payload"] as stream:
                response_value = await stream.read()
                return response_value.decode("utf-8")
        except ClientError as e:
            print(e)
        except Exception as e:
            code = type(e).__name__
            messsage = f"invoke_lambda caused unhandled exception: {code}: {str(e)}"
            print(messsage)
            raise e

    async def naive_invoke(self):
        """
        Do **NOT** do this. It's pretty tempting and "obvious", but it will
        basically give the same throughput as a blocking call because no
        additional concurrency is achieved as the results for each invocation
        are awaited before the next iteration of the loop may begin. This
        naive approach is included here primarily as an illustration as it's
        quite easy to do this sort of thing without thinking about why one
        might wish to use asyncio in the first place.
        """

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

        for i in range(0, self.iterations):
            body = json_payload if json_payload != None else json.dumps({"Hello": str(uuid.uuid4())})
            results = await self.invoke_lambda(body)
            #print(results)

        end = time.time()
        print(self.iterations/(end - start))

    async def invoke_as_tasks_in_batches(self):
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

        for i in range(0, self.iterations, MAX_WORKERS):
            remaining = self.iterations - i
            task_size = MAX_WORKERS if remaining > MAX_WORKERS else remaining
            tasks = []
            for j in range(task_size):
                body = json_payload if json_payload != None else json.dumps({"Hello": str(uuid.uuid4())})
                tasks.append(self.invoke_lambda(body))
            results = await asyncio.gather(*tasks)
            print(results)

        end = time.time()
        print(self.iterations/(end - start))

if __name__ == '__main__':
    client = EchoClient(function_name="echo-lambda")
    loop = asyncio.get_event_loop()
    #loop.run_until_complete(client.naive_invoke())
    loop.run_until_complete(client.invoke_as_tasks_in_batches())

