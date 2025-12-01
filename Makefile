
ARCH=amd64
#ARCH=arm64
DESTINATION:= bin/

all: format lint daemon server


# Get the version out of config.json
# Finds the line containing "version": "…"
# Extracts the quoted value
# Uses only sed regex — safe, portable, works on Linux & macOS
VERSION := $(shell sed -n 's/.*"version":[[:space:]]*"\([^"]*\)".*/\1/p' config.json)
$(info VERSION is $(VERSION))

# Setting CGO_ENABLED=0 allows creation of a statically linked executable.
# ldflags -s -w disables DWARF & symbol table generation to reduce binary size
# ldflags -X Sets the string variable in the importpath named name to value
# E.g. -ldflags "-X lambda-runtime-api-daemon/pkg/config/rapid.version=1.2.3"
# will set the version variable in the rapid package to 1.2.3
# See https://pkg.go.dev/cmd/link
# This seems to be a fairly common approach for embedding version info in go
# https://www.digitalocean.com/community/tutorials/using-ldflags-to-set-version-information-for-go-applications?utm_source=chatgpt.com
# https://leapcell.io/blog/advanced-go-linker-usage-injecting-version-info-and-build-configurations?utm_source=chatgpt.com
daemon: format
	CGO_ENABLED=0 GOOS=linux GOARCH=${ARCH} go build -ldflags "-s -w -X lambda-runtime-api-daemon/pkg/config/rapid.version=$(VERSION)" -o ${DESTINATION} ./cmd/lambda-runtime-api-daemon

server: format
	CGO_ENABLED=0 GOOS=linux GOARCH=${ARCH} go build -ldflags "-s -w -X lambda-runtime-api-daemon/pkg/config/server.version=$(VERSION)" -o ${DESTINATION} ./cmd/lambda-server

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

.PHONY: all clean clean-dep dep docker format lint test

