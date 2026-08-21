.PHONY: build test verify

build:
	GOWORK=off go build -o bin/hami-cli .

test:
	GOWORK=off go test ./...

verify: test
	GOWORK=off go vet ./...
	git diff --check
