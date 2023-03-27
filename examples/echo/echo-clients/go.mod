module rpcclient

go 1.18

// Use relative path to lambda-runtime-api-daemon as we want to reuse the
// AMQP wrapper in the messaging sub package
replace lambda-runtime-api-daemon => ../../../

require (
	github.com/docker/distribution v2.8.1+incompatible
	lambda-runtime-api-daemon v0.0.0-00010101000000-000000000000
)

require (
	github.com/rabbitmq/amqp091-go v1.7.0 // indirect
	github.com/sirupsen/logrus v1.9.0 // indirect
	golang.org/x/sys v0.0.0-20220715151400-c0bba94af5f8 // indirect
)
