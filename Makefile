BINARY=bin/agent
CONFIG=config.yaml

.PHONY: build run stop restart logs clean lint test

build:
	go build -o $(BINARY) ./cmd/agent/

run: build
	mkdir -p data
	-pkill -x agent
	nohup ./$(BINARY) $(CONFIG) > data/stdout.log 2>&1 &
	sleep 1
	tail -10 data/agent.log

stop:
	-pkill -x agent
	@echo "Stopped."

restart: stop
	sleep 1
	$(MAKE) run

logs:
	tail -f data/agent.log

clean:
	rm -f $(BINARY)

lint:
	go fix ./...
	go fmt ./...
	golangci-lint run ./...

test:
	go test ./...
