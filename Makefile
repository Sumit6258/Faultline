.PHONY: help setup test test-race lint bench run clean

LABS := rate-limiting caching consistent-hashing distributed-locks leader-election circuit-breakers retries-backoff replication kafka-messaging
SYSTEMS := url-shortener

help:
	@echo "Available targets:"
	@echo "  make setup           install Go if it's missing (Debian/Ubuntu)"
	@echo "  make test            run tests for every lab and system"
	@echo "  make test-race       run tests with the race detector"
	@echo "  make lint            run go vet and gofmt checks"
	@echo "  make bench LAB=x     run benchmarks for one lab"
	@echo "  make bench SYSTEM=x  run benchmarks for one system"
	@echo "  make run LAB=x       run a lab's demo binary"
	@echo "  make run SYSTEM=x    run a system's server"
	@echo "  make clean           remove build artifacts"

setup:
	@command -v go >/dev/null 2>&1 && echo "Go is already installed: $$(go version)" || (sudo apt-get update && sudo apt-get install -y golang-go)

test:
	@for lab in $(LABS); do \
		echo "=== testing labs/$$lab ==="; \
		(cd labs/$$lab && go test ./...) || exit 1; \
	done
	@for sys in $(SYSTEMS); do \
		echo "=== testing systems/$$sys ==="; \
		(cd systems/$$sys && go test ./...) || exit 1; \
	done

test-race:
	@for lab in $(LABS); do \
		echo "=== testing labs/$$lab with -race ==="; \
		(cd labs/$$lab && go test -race ./...) || exit 1; \
	done
	@for sys in $(SYSTEMS); do \
		echo "=== testing systems/$$sys with -race ==="; \
		(cd systems/$$sys && go test -race ./...) || exit 1; \
	done

lint:
	@for lab in $(LABS); do \
		echo "=== linting labs/$$lab ==="; \
		(cd labs/$$lab && go vet ./... && test -z "$$(gofmt -l .)") || exit 1; \
	done
	@for sys in $(SYSTEMS); do \
		echo "=== linting systems/$$sys ==="; \
		(cd systems/$$sys && go vet ./... && test -z "$$(gofmt -l .)") || exit 1; \
	done

bench:
	@if [ -n "$(SYSTEM)" ]; then \
		cd systems/$(SYSTEM) && go test -bench=. -benchmem ./...; \
	elif [ -n "$(LAB)" ]; then \
		cd labs/$(LAB) && go test -bench=. -benchmem ./...; \
	else \
		echo "usage: make bench LAB=rate-limiting  or  make bench SYSTEM=url-shortener"; exit 1; \
	fi

run:
	@if [ -n "$(SYSTEM)" ]; then \
		cd systems/$(SYSTEM) && go run ./cmd/server; \
	elif [ -n "$(LAB)" ]; then \
		cd labs/$(LAB) && go run ./cmd/...; \
	else \
		echo "usage: make run LAB=rate-limiting  or  make run SYSTEM=url-shortener"; exit 1; \
	fi

clean:
	@for lab in $(LABS); do \
		(cd labs/$$lab && go clean); \
	done
	@for sys in $(SYSTEMS); do \
		(cd systems/$$sys && go clean); \
	done
