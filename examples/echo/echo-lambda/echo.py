import os
#import sys

#test = [1, 2, 3]
#print(test[3]) # Deliberately exception to test init error

def handler(event, context):
    #print(event)
    #print(context)
    #print(context.get_remaining_time_in_millis())
    #test = [1, 2, 3]
    #print(test)
    #print(test[3]) # Deliberately exception to test invocation error
    #print(os.environ)
    #print(os.getcwd())
    # In a real Lambda **** DO WORK HERE ****
    return event  # Default to simple echo processor
