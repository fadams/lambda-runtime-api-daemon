
ARCH=amd64
#ARCH=arm64
DESTINATION:= bin/

all: format lint daemon server

# Setting CGO_ENABLED=0 allows creation of a statically linked executable.
# ldflags -s -w disables DWARF & symbol table generation to reduce binary size
daemon: format
	CGO_ENABLED=0 GOOS=linux GOARCH=${ARCH} go build -ldflags "-s -w" -o ${DESTINATION} ./cmd/lambda-runtime-api-daemon

server: format
	CGO_ENABLED=0 GOOS=linux GOARCH=${ARCH} go build -ldflags "-s -w" -o ${DESTINATION} ./cmd/lambda-server

# Use Docker to build the daemon and server
docker:
	docker build -t lambda-runtime-api-daemon -f docker/Dockerfile-daemon .
	docker build -t lambda-server -f docker/Dockerfile-server .
	# Once the images are built extract the executables and copy to the bin
	# directory using docker create to create a container and docker cp to
	# copy the required file from the container.
	mkdir -p bin
	dummy=$$(id=$$(docker create lambda-runtime-api-daemon); docker cp $$id:/usr/bin/lambda-runtime-api-daemon bin/lambda-runtime-api-daemon; docker rm -v $$id)
	dummy=$$(id=$$(docker create lambda-server); docker cp $$id:/usr/bin/lambda-server bin/lambda-server; docker rm -v $$id)

format:
	go fmt ./...

lint:
	go vet ./...

# Install module dependencies
dep:
	go mod tidy

clean:
	rm -rf bin

clean-dep:
	rm -f go.sum

test:
	go test -v ./...

.PHONY: all clean clean-dep dep docker format lint

