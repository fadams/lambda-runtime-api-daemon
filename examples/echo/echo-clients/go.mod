module rpcclient

go 1.22

// Use relative path to lambda-runtime-api-daemon as we want to reuse the
// AMQP wrapper in the messaging sub package
replace lambda-runtime-api-daemon => ../../../

require (
	github.com/docker/distribution v2.8.3+incompatible
	lambda-runtime-api-daemon v0.0.0-00010101000000-000000000000
)

require github.com/rabbitmq/amqp091-go v1.10.0 // indirect
