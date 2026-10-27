VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/r0jjames/bam-cli/internal/cli.version=$(VERSION)

.PHONY: build install test lint e2e record check-fixtures docs stub release

build:
	go build -ldflags "$(LDFLAGS)" -o bin/bam ./cmd/bam

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/bam

test:
	go test ./...

lint:
	golangci-lint run

e2e:
	go test -tags e2e ./e2e/... -args $(ARGS)

record:
	go run ./tools/record $(ARGS)

check-fixtures:
	go test ./internal/provider/bamboo -run TestFixtureGuard

docs:
	go run ./tools/gendocs

stub:
	go run ./tools/bamboostub $(ARGS)

release:
	./scripts/release.sh $(VERSION)
