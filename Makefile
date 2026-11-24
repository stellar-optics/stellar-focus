test:
	go test -v -race ./...

test-props:
	go test ./pkg/focus -run 'TestProperty' -count=1 -v

lint:
	golangci-lint run

fmt:
	go fmt ./...

build:
	go build ./...

