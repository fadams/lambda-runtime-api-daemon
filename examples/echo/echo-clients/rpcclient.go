/*
To generate module dependencies:
go mod tidy

To compile (CGO_ENABLED=0 allows creation of a statically linked executable):
CGO_ENABLED=0 go build rpcclient.go

ldflags -s -w disables DWARF & symbol table generation to reduce binary size
CGO_ENABLED=0 go build -ldflags "-s -w" rpcclient.go

Check what the escape analysis is doing by running with
CGO_ENABLED=0 go build -gcflags="-m" rpcclient.go
If you run this, Go will tell you if a variable will escape to the heap or not
*/

package main

import (
	//"context"
	"fmt"
	"log"
	"os"
	//"runtime/pprof"
	"time"

	// https://pkg.go.dev/github.com/docker/distribution/uuid
	"github.com/docker/distribution/uuid"

	"lambda-runtime-api-daemon/pkg/messaging"
)

func failOnError(err error, msg string) {
	if err != nil {
		log.Panicf("%s: %s", msg, err)
	}
}

func main() {
	/*
	   // Enable profiling
	   f, err := os.Create("cpuprofile")
	   if err != nil {
	       log.Fatal(err)
	   }
	   pprof.StartCPUProfile(f)
	   defer pprof.StopCPUProfile()
	*/

	amqpURI := "amqp://guest:guest@localhost:5672?connection_attempts=20&retry_delay=10&heartbeat=0"
	// Use os.LookupEnv not os.Getenv to cater for unset environment variables.
	if value, ok := os.LookupEnv("AMQP_URI"); ok {
		amqpURI = value
	}

	//iterations := 40000000
	//iterations := 4000000
	//iterations := 1000000
	//iterations := 100000
	//iterations := 10000
	iterations := 10
	count := 0
	done := make(chan struct{})
	startTime := time.Now()

	connection, err := messaging.NewConnection(amqpURI)
	failOnError(err, "Failed to connect to RabbitMQ")
	defer connection.Close()

	go func() {
		err := <-connection.CloseNotify() // Blocks goroutine until notified of an error
		fmt.Println("------------ OnClose triggered ---------------")
		fmt.Println(err)
		fmt.Println("----------------------------------------------")
	}()

	//session, err := connection.Session()
	session, err := connection.Session(messaging.AutoAck)
	failOnError(err, "Failed to open Session")
	defer session.Close()

	// Increase the consumer priority of the reply_to consumer.
	// See https://www.rabbitmq.com/consumer-priority.html
	// N.B. This syntax uses the JMS-like Address String which gets parsed into
	// implementation specific constructs. The link/x-subscribe is an
	// abstraction for AMQP link subscriptions, which in AMQP 0.9.1 maps to
	// channel.basic_consume and allows us to pass the exclusive and arguments
	// parameters. NOTE HOWEVER that setting the consumer priority is RabbitMQ
	// specific and it might well not be possible to do this on other providers.
	replyTo, err := session.Consumer(
		`; {"link": {"x-subscribe": {"arguments": {"x-priority": 10}}}}`,
	)
	failOnError(err, "Failed to create Consumer")

	replyTo.SetCapacity(100)

	producer, err := session.Producer("")
	failOnError(err, "Failed to create Producer")

	rpcResponse := func(message messaging.Message) {
		//log.Printf("rpcResponse: %s", message.Body)
		log.Printf("%s", message.Body)
		//log.Printf(message.CorrelationID)
	}

	msgs := replyTo.Consume()
	go func() {
		for m := range msgs {
			rpcResponse(m)
			count++
			if count == iterations {
				fmt.Println()
				fmt.Println("Test complete")
				duration := time.Since(startTime).Seconds()
				fmt.Printf("Throughput: %f items/s\n", float64(iterations)/duration)
				fmt.Println()
				done <- struct{}{}
				break
			}
		}
	}()

	log.Printf("Starting test")

	startTime = time.Now()
	for i := 0; i < iterations; i++ {
		correlationID := uuid.Generate().String()

		// Ensure these strings are JSON otherwise Lambda fails with
		// Runtime.UnmarshalError
		//body := `"Hello World!"`
		body := fmt.Sprintf(`{"Hello": "%v"}`, correlationID)

		producer.Send(messaging.Message{
			Subject:       "echo-lambda",
			ContentType:   "application/json",
			ReplyTo:       replyTo.Name(),
			CorrelationID: correlationID,
			//Durable:     true,
			Body: []byte(body),
			//Mandatory: true,
		})
	}

	producer.Close()

	<-done
}
