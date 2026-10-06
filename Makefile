.PHONY: build test integration fmt vet
build:
	go build -trimpath -o bin/nimbus ./cmd/nimbus
test:
	go test -race ./...
integration:
	@test -n "$(TEST_DATABASE_URL)" || (echo 'Set TEST_DATABASE_URL to a disposable PostgreSQL database'; exit 1)
	go test -race -count=1 ./internal/control
fmt:
	gofmt -w cmd internal
vet:
	go vet ./...
