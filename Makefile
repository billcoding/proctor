APP=proctor
VERSION?=0.1.0
LDFLAGS=-s -w -X github.com/billcoding/proctor/internal/agent.Version=$(VERSION)

.PHONY: all deps build agent server build-all clean run-server run-agent

all: build

deps:
	go mod tidy

build: agent server

agent:
	go build -ldflags "$(LDFLAGS)" -o bin/proctor-agent ./cmd/agent

server:
	go build -ldflags "$(LDFLAGS)" -o bin/proctor-server ./cmd/server

build-all: deps
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/darwin-arm64/proctor-agent ./cmd/agent
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/darwin-arm64/proctor-server ./cmd/server
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/darwin-amd64/proctor-agent ./cmd/agent
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/darwin-amd64/proctor-server ./cmd/server
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/linux-amd64/proctor-agent ./cmd/agent
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/linux-amd64/proctor-server ./cmd/server
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/linux-arm64/proctor-agent ./cmd/agent
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/linux-arm64/proctor-server ./cmd/server
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/windows-amd64/proctor-agent.exe ./cmd/agent
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/windows-amd64/proctor-server.exe ./cmd/server

run-server: server
	./bin/proctor-server -config ./configs/server.json

run-agent: agent
	./bin/proctor-agent run -config ./configs/agent.json

clean:
	rm -rf bin dist data
