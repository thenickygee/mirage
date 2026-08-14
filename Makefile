.PHONY: build run lint fmt install

build:
	go build -o mirage .

run:
	go run main.go

fmt:
	goimports -w .

lint:
	golangci-lint run ./...

install:
	./install.sh
