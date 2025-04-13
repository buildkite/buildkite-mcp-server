default: build

PHONY: build
build:
	go build -o buildkite-mcp-server ./cmd/buildkite-mcp-server/main.go

snapshot:
	goreleaser build --snapshot --clean  --single-target

.PHONY: run
run:
	go run cmd/buildkite-mcp-server/main.go stdio

.PHONY: test
test:
	go test -coverprofile coverage.out -covermode atomic -v ./...

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: lint-fix
lint-fix:
	golangci-lint run --fix ./...

.PHONY: update-docs
update-docs:
	go run cmd/update-docs/main.go
