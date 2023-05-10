# This illustrates a "full" Internal Extension e.g. one that registers for
# events as opposed to just a wrapper script.
#
# For the case of Python this is _vaguely_ pointless, because all of the
# information available to the "INVOKE" Extension event (and more) is available
# to the Lambda handler, and the "SHUTDOWN" event can't be registered by an
# internal Extension. The main benefit of having Internal Extensions register,
# even with {"events": []}, is because with events registered the Runtime will
# be sent SIGTERM. Unfortunately this does not work on Python 3.8 and 3.9
# Runtimes (see link below) as they use native C++ code to service the "next"
# API requests which blocks, preventing signals from being propagated to the
# Python interpreter.
# https://github.com/aws-samples/graceful-shutdown-with-aws-lambda

import json, os, requests, sys, threading, time

LAMBDA_EXTENSION_NAME = "internal"

# Create a requests Session so that the underlying TCP connection is reused
# https://requests.readthedocs.io/en/latest/user/advanced/
session = requests.Session()

def register_extension():
    print(f"[{LAMBDA_EXTENSION_NAME}] Registering...", flush=True)

    #response = requests.post(
    response = session.post(
        url=f"http://{os.environ['AWS_LAMBDA_RUNTIME_API']}/2020-01-01/extension/register",
        # Can't register SHUTDOWN from Internal Extensions
        #json={'events': ['INVOKE', 'SHUTDOWN']},
        json={'events': ['INVOKE']},
        headers={'Lambda-Extension-Name': LAMBDA_EXTENSION_NAME}
    )
    ext_id = response.headers['Lambda-Extension-Identifier']
    print(f"[{LAMBDA_EXTENSION_NAME}] Registered with ID: {ext_id}", flush=True)

    return ext_id

def process_events(ext_id):
    while True:
        print(f"[{LAMBDA_EXTENSION_NAME}] Waiting for event...", flush=True)
        #response = requests.get(
        response = session.get(
            url=f"http://{os.environ['AWS_LAMBDA_RUNTIME_API']}/2020-01-01/extension/event/next",
            headers={'Lambda-Extension-Identifier': ext_id},
            timeout=None
        )
        #print(response.text)
        event = json.loads(response.text)
        if event['eventType'] == 'SHUTDOWN':
            print(f"[{LAMBDA_EXTENSION_NAME}] Received SHUTDOWN event. Exiting.", flush=True)
            sys.exit(0)
        else:
            print(f"[{LAMBDA_EXTENSION_NAME}] Received event: {json.dumps(event)}", flush=True)

# execute extensions logic
extension_id = register_extension()
threading.Thread(target=process_events, args=(extension_id,)).start()

def lambda_handler(event, context):
    # TODO implement

    return {
        'statusCode': 200,
        'body': json.dumps('Hello from Lambda!')
    }

