
ARCH=amd64
#ARCH=arm64
DESTINATION:= ./

all: format lint rpcclient

# Setting CGO_ENABLED=0 allows creation of a statically linked executable.
# ldflags -s -w disables DWARF & symbol table generation to reduce binary size
rpcclient: format
	CGO_ENABLED=0 GOOS=linux GOARCH=${ARCH} go build -ldflags "-s -w" -o ${DESTINATION} rpcclient

# Use Docker to build the rpcclient. Note that the Dockerfile is in the docker
# subdirectory of the repository root directory. This is important as the
# docker context needs access to the pkg/messaging directory as rpcclient
# uses that package.
docker:
	cd ../../../ && docker build -t rpcclient -f docker/Dockerfile-rpcclient .
	dummy=$$(id=$$(docker create rpcclient); docker cp $$id:/usr/bin/rpcclient rpcclient; docker rm -v $$id)

format:
	go fmt ./...

lint:
	go vet ./...

# Install module dependencies
dep:
	go mod tidy

clean:
	rm -rf rpcclient
	rm -rf *~

clean-dep:
	rm -f go.sum

.PHONY: all clean clean-dep dep docker format lint

