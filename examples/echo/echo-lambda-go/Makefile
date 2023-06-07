
ARCH=amd64
#ARCH=arm64
DESTINATION:= ./

# Default to use musl-gcc for compiling entrypoint to minimise binary size
# If gcc is required this may be overridden by setting the CC variable, e.g.
# make CC=gcc entrypoint
CC=musl-gcc

all: format lint entrypoint echo

# Setting CGO_ENABLED=0 allows creation of a statically linked executable.
# ldflags -s -w disables DWARF & symbol table generation to reduce binary size
#
# The lambda.norpc tag drops the (legacy) RPC mode dependencies which results
# in a smaller binary size and user space surface. Legacy RPC mode is not
# required as we are exclusively using the Lambda Runtime API.
echo: format
	CGO_ENABLED=0 GOOS=linux GOARCH=${ARCH} go build -tags lambda.norpc -ldflags "-s -w" -o ${DESTINATION} echo

entrypoint:
	$(CC) -Os -s -static lambda-entrypoint/entrypoint.c -o entrypoint

# Use Docker to build the echo lambda.
docker:
	docker build -t echo-lambda-go .

format:
	go fmt ./...

lint:
	go vet ./...

# Install module dependencies
dep:
	go mod tidy

clean:
	rm -rf echo
	rm -rf entrypoint
	rm -rf *~

clean-dep:
	rm -f go.sum

.PHONY: all clean clean-dep dep docker format lint

