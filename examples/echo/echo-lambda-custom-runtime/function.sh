# A simple echo Lambda implemented in bash
function handler () {
    EVENT_DATA=$1
    CONTEXT=$2 # Really the HTTP headers from the Runtime API next response.

    # echo the Lambda event to stderr
    #echo "$EVENT_DATA" 1>&2;

    # echo the Lambda context to stderr
    #echo "$CONTEXT" 1>&2;

    RESPONSE="$EVENT_DATA" # For this Lambda simply echo the EVENT_DATA

    echo $RESPONSE
}
