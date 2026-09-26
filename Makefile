CGO_ENABLED := 1
export CGO_ENABLED

.PHONY: all build mock rulesdump test vet fmt clean run-dev deb

all: build

build:
	go build -o gosnitch ./cmd/gosnitch

mock:
	go build -o mockdaemon ./cmd/mockdaemon

rulesdump:
	go build -o rulesdump ./cmd/rulesdump

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

clean:
	rm -f gosnitch mockdaemon rulesdump usr/bin/gosnitch
	rm -rf pkg

# Build the .deb into pkg/ via ian. The binary must land in the install tree
# first; ian packages what the working tree holds, minus .ianignore.
deb:
	go build -o usr/bin/gosnitch ./cmd/gosnitch
	ian pkg

# Run the UI and a fake daemon against a throwaway socket.
run-dev: build mock
	./gosnitch -address unix:///tmp/gosnitch-test.sock & \
	sleep 2; ./mockdaemon -address unix:///tmp/gosnitch-test.sock
