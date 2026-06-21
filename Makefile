.PHONY: default build check vet tidy clean

default: vet build

build:
	go build -o bin/proxyproxy ./cmd/proxyproxy

check:
	go build ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -f bin/proxyproxy
