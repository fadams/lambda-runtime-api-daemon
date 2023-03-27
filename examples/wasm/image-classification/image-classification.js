"use strict";

const { spawn } = require('child_process');
const path = require('path');

// This function spawns the WasmEdge https://wasmedge.org/ WebAssembly
// runtime in a child process that launches image-greyscale.so IOT compiled
// WebAssembly application. The input reqBody Uint8Array is written to
// the WasmEdge runtime's stdin and its stdout is collected into a string.
function _runWasm(reqBody) {
    return new Promise(resolve => {
        const wasmedge = spawn(
            path.join(__dirname, 'wasmedge-tensorflow-lite'),
            [path.join(__dirname, 'image-classification.so')],
            {env: {'LD_LIBRARY_PATH': __dirname}}
        );

        let d = [];
        wasmedge.stdout.on('data', (data) => {
            d.push(data);
        });

        wasmedge.on('close', (code) => {
            resolve(d.join(''));
        });

        wasmedge.stdin.write(reqBody);
        wasmedge.stdin.end('');
    });
}

exports.handler = async (event, context) => {
    //console.log("EVENT: " + JSON.stringify(event, null, 2));

    // Check the event.body is hex, convert to int then collect
    // into a Uint8Array TypedArray.
    var typedArray = new Uint8Array(event.body.match(/[\da-f]{2}/gi).map(function (h) {
        return parseInt(h, 16);
    }));

    // This actually runs the main image classification function
    let result = await _runWasm(typedArray);

    // Return object using AWS API Gateway Lambda proxy integration syntax
    // https://docs.aws.amazon.com/apigateway/latest/developerguide/set-up-lambda-proxy-integrations.html#api-gateway-simple-proxy-for-lambda-output-format
    return {
        statusCode: 200,
        headers: {
            "Access-Control-Allow-Headers" : "Content-Type,X-Amz-Date,Authorization,X-Api-Key,X-Amz-Security-Token",
            "Access-Control-Allow-Origin": "*",
            "Access-Control-Allow-Methods": "DELETE, GET, HEAD, OPTIONS, PATCH, POST, PUT"
        },
        body: result
    };
}
