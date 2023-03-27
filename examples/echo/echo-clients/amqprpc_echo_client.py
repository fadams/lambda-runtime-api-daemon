#!/usr/bin/env -S PYTHONPATH=../..  python3
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
# PYTHONPATH=../.. python3 echo_client_asyncio.py
# PYTHONPATH=../.. LOG_LEVEL=DEBUG python3 echo_client_asyncio.py
#
# PYTHONPATH is used to find the utils module
#
"""
An echo client intended to test the base AMQP Message RPC Lambda.

The client times a number of iterations of publishing request messages as fast
as it can, then asynchronously receives the results from the processor and
correlates them with the original request.

The timer starts as the first request is published and stops as the last
response is received so the overall calculated RPC invocation rate will take
into account the effects of any queueing that may be a result of the processor
not keeping up with the invocation rate.
"""

import sys
assert sys.version_info >= (3, 6) # Bomb out if not running Python3.6

import asyncio, time, uuid

from utils.logger import init_logging
from utils.amqp_0_9_1_messaging_asyncio import Connection, Message
from utils.messaging_exceptions import *

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

"""
TODO
# Attempt to use libuuid uuid generation if available.
# https://github.com/brandond/python-libuuid/
# https://pypi.org/project/libuuid/
# pip3 install libuuid
# N.B. needs Linux distro uuid-dev package installed
"""

class EchoClient(object):
    #def __init__(self, queue_name, iterations=1000000):
    #def __init__(self, queue_name, iterations=100000):
    #def __init__(self, queue_name, iterations=10000):
    def __init__(self, queue_name, iterations=10):
    #def __init__(self, queue_name, iterations=1):
        self.queue_name = queue_name  # queue_name is the Lambda's RPC queue
        # Initialise logger
        self.logger = init_logging(log_name=queue_name)
        self.iterations = iterations
        self.count = 0  # Counts the number of responses
        self.start_time = 0
        # start_asyncio() actually opens the Connection
        self.connection = None

        """
        In order to deal with RPC we need to be able to associate requests
        with their subsequent responses, so this pending_requests dictionary
        maps requests with their callbacks using correlation IDs.
        """
        self.pending_requests = {}

    def handle_rpcmessage_response(self, message):
        #print(message)
        #print(message.body)
        self.count += 1
        #print(self.count)

        # Safer but slower alternative to connection.session(auto_ack=True)
        #if self.count % 10 == 0:  # Periodically acknowledge consumed messages
        #    message.acknowledge()

        """
        This is a message listener receiving messages from the reply_to queue
        for this workflow engine instance.
        TODO cater for the case where requests are sent but responses never
        arrive, this scenario will cause self.pending_requests to "leak" as
        correlation_id keys get added but not removed. This situation should be
        improved as we add code to handle Task state "rainy day" scenarios such
        as Timeouts etc. so park for now, but something to be aware of.
        """
        correlation_id = message.correlation_id
        request = self.pending_requests.get(correlation_id)  # Get request tuple
        if request:
            del self.pending_requests[correlation_id]
            callback = request
            if callable(callback):
                message_body = message.body
                callback(message_body, correlation_id)

        if self.count == self.iterations:
            print()
            print("Test complete")
            duration = time.time() - self.start_time
            print("Throughput: {} items/s".format(self.iterations/duration))
            print()
            self.connection.close()

    def send_rpcmessage(self, body, content_type="application/json", correlation_id=None):
        """
        Publish message to the required Lambda's RPC queue.
        """

        """
        Simple callback to handle RPC response. We store this in the
        pending_requests dict keyed by correlation_id so that the
        handle_rpcmessage_response message listener that is triggered by
        AMQP response messages can use the returned correlation_id to look
        up the pending_requests dict to retrieve the on_response callback
        instance for a given request.
        """
        def on_response(result, correlation_id):
            print(result)
            #print(correlation_id)
            pass
        
        # Associate response callback with this request via correlation ID
        # Use the optional correlation_id argument if supplied otherwise
        # create one. The optional correlation_id argument is useful to avoid
        # creating another UUID if the supplied request body already happens to
        # have its own UUID
        # TODO faster UUID generation using Cython and libuuid because at high
        # throughput UUID generation can cause around 10% performance hit.
        correlation_id = str(uuid.uuid4()) if correlation_id == None else correlation_id

        #print("send_rpcmessage")
        #print(body)
        #print(correlation_id)
        #print(content_type)
        
        message = Message(
            body,
            content_type=content_type,
            reply_to=self.reply_to.name,
            correlation_id=correlation_id,
        )

        """
        The service response message is handled by handle_rpcmessage_response()
        Set request tuple keyed by correlation_id so we can look up the
        required callback
        """
        self.pending_requests[correlation_id] = (
            on_response
        )

        """
        When producer.enable_exceptions(True) is set the send() method is made
        awaitable by returning a Future, which we return to the caller.
        """
        return self.producer.send(message)

    async def run(self):
        self.connection = Connection("amqp://localhost:5672?connection_attempts=20&retry_delay=10&heartbeat=0")
        try:
            await self.connection.open()
            #session = await self.connection.session()
            session = await self.connection.session(auto_ack=True)   
            """
            Increase the consumer priority of the reply_to consumer.
            See https://www.rabbitmq.com/consumer-priority.html
            N.B. This syntax uses the JMS-like Address String which gets parsed into
            implementation specific constructs. The link/x-subscribe is an
            abstraction for AMQP link subscriptions, which in AMQP 0.9.1 maps to
            channel.basic_consume and allows us to pass the exclusive and arguments
            parameters. NOTE HOWEVER that setting the consumer priority is RabbitMQ
            specific and it might well not be possible to do this on other providers.
            """
            self.reply_to = await session.consumer(
                '; {"link": {"x-subscribe": {"arguments": {"x-priority": 10}}}}'
            )

            # Enable consumer prefetch
            self.reply_to.capacity = 100;
            await self.reply_to.set_message_listener(self.handle_rpcmessage_response)
            self.producer = await session.producer(self.queue_name)
            self.producer.enable_exceptions(sync=True)

            """
            Send the same payload each message. It's not very representative,
            but the main aim is to go fast to test processor performance.
            """
            #payload = {"payload": "Hello World"}
            #payload = {"Hello": str(uuid.uuid4())}
            json_payload = None
            #json_payload = json.dumps(payload)
            #json_payload = '"Hello World"'
            #json_payload = '{"payload": "Hello World"}'
            #json_payload = "x" * 50000

            waiters = []
            self.start_time = time.time()
            for i in range(self.iterations):
                try:
                    """
                    When producer.enable_exceptions(True) is set the send()
                    method is made awaitable by returning a Future, resolved
                    when the broker acks the message or exceptioned if the
                    broker nacks. For fully "synchronous" publishing one can
                    simply do: await self.producer.send(message)
                    but that has a serious throughput/latency impact waiting
                    for the publisher-confirms from the broker. The impact is
                    especially bad for the case of durable queues.
                    https://www.rabbitmq.com/confirms.html#publisher-confirms
                    https://www.rabbitmq.com/confirms.html#publisher-confirms-latency
                    To mitigate this the example below publishes in batches
                    (optimally the batch size should be roughly the same as
                    the session capacity). We store the awaitables in a list
                    then periodically do an asyncio.gather of the waiters
                    """
                    id = None
                    body = json_payload
                    if json_payload == None:
                        id = str(uuid.uuid4())
                        body = json.dumps({"Hello": id})
                    
                    waiters.append(self.send_rpcmessage(body, correlation_id=id))
                    if len(waiters) == 100:
                        await asyncio.gather(*waiters)
                        #await asyncio.sleep(0) # Yield the asyncio event loop
                        waiters = []
                    
                    """
                    When producer.enable_exceptions(False) is set the send()
                    method will raise an exception that contains the sequence
                    numbers of messages that have failed to publish. If no
                    call to enable_exceptions() is made send() just does
                    basic_publish() with no confirmation. The asyncio.sleep(0)
                    in the example is necessary to periodically yield to the
                    asyncio event loop to allow consumer delivery.
                    """
                    """
                    self.send_rpcmessage(body, correlation_id=id)
                    if i % 100 == 0:
                        await asyncio.sleep(0) # Yield to the asyncio event loop
                    """

                except SendError as e:
                    #print(waiters)
                    raise e

            await self.connection.start(); # Wait until connection closes.
    
        except MessagingError as e:  # ConnectionError, SessionError etc.
            self.logger.info(e)

        self.connection.close()

if __name__ == '__main__':
    client = EchoClient(queue_name="echo-lambda")
    loop = asyncio.get_event_loop()
    loop.run_until_complete(client.run())
    #loop.close()
       
