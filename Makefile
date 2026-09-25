CGO_ENABLED := 1
export CGO_ENABLED

.PHONY: all build mock rulesdump test vet fmt clean run-dev

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
	rm -f gosnitch mockdaemon rulesdump

# Run the UI and a fake daemon against a throwaway socket.
run-dev: build mock
	./gosnitch -address unix:///tmp/gosnitch-test.sock & \
	sleep 2; ./mockdaemon -address unix:///tmp/gosnitch-test.sock
