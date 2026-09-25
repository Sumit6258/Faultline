.PHONY: help setup test test-race lint bench run clean

LABS := rate-limiting caching consistent-hashing distributed-locks leader-election circuit-breakers retries-backoff replication kafka-messaging

help:
	@echo "Available targets:"
	@echo "  make setup        install Go if it's missing (Debian/Ubuntu)"
	@echo "  make test         run tests for every lab"
	@echo "  make test-race    run tests with the race detector"
	@echo "  make lint         run go vet and gofmt checks"
	@echo "  make bench LAB=x  run benchmarks for one lab"
	@echo "  make run LAB=x    run a lab's demo binary"
	@echo "  make clean        remove build artifacts"

setup:
	@command -v go >/dev/null 2>&1 && echo "Go is already installed: $$(go version)" || (sudo apt-get update && sudo apt-get install -y golang-go)

test:
	@for lab in $(LABS); do \
		echo "=== testing labs/$$lab ==="; \
		(cd labs/$$lab && go test ./...) || exit 1; \
	done

test-race:
	@for lab in $(LABS); do \
		echo "=== testing labs/$$lab with -race ==="; \
		(cd labs/$$lab && go test -race ./...) || exit 1; \
	done

lint:
	@for lab in $(LABS); do \
		echo "=== linting labs/$$lab ==="; \
		(cd labs/$$lab && go vet ./... && test -z "$$(gofmt -l .)") || exit 1; \
	done

bench:
	@test -n "$(LAB)" || (echo "usage: make bench LAB=rate-limiting" && exit 1)
	cd labs/$(LAB) && go test -bench=. -benchmem ./...

run:
	@test -n "$(LAB)" || (echo "usage: make run LAB=rate-limiting" && exit 1)
	cd labs/$(LAB) && go run ./cmd/...

clean:
	@for lab in $(LABS); do \
		(cd labs/$$lab && go clean); \
	done
