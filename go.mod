module lambda-runtime-api-daemon

go 1.22

require (
	github.com/docker/distribution v2.8.3+incompatible
	github.com/go-chi/chi/v5 v5.0.12
	github.com/rabbitmq/amqp091-go v1.10.0
	go.uber.org/zap v1.27.1
	golang.org/x/sys v0.20.0
)

require go.uber.org/multierr v1.10.0 // indirect
