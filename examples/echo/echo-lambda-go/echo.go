package main

import (
	"context"
	//"fmt"
	"github.com/aws/aws-lambda-go/lambda"
)

/*type MyEvent struct {
	Name string `json:"name"`
}

func Handler(ctx context.Context, name MyEvent) (string, error) {
	return fmt.Sprintf("Hello %s!", name.Name), nil
}*/

func EchoHandler(ctx context.Context, event any) (any, error) {
	return event, nil
}

func main() {
	lambda.Start(EchoHandler)
}
