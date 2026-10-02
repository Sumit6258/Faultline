.PHONY: help setup test test-race lint bench run run-web clean

LABS := rate-limiting caching consistent-hashing distributed-locks leader-election circuit-breakers retries-backoff replication kafka-messaging
SYSTEMS_GO := url-shortener
SYSTEMS_PYTHON := payment-platform
WEB := interactive-lab

help:
	@echo "Available targets:"
	@echo "  make setup           install Go if it's missing (Debian/Ubuntu)"
	@echo "  make test            run tests for every lab and system"
	@echo "  make test-race       run tests with the race detector (Go only)"
	@echo "  make lint            run go vet/gofmt on Go, a compile check on Python"
	@echo "  make bench LAB=x     run benchmarks for one Go lab"
	@echo "  make bench SYSTEM=x  run benchmarks for one system, Go or Python"
	@echo "  make run LAB=x       run a lab's demo binary"
	@echo "  make run SYSTEM=x    run a system's server"
	@echo "  make run-web         start the interactive lab's dev server"
	@echo "  make clean           remove build artifacts"

setup:
	@command -v go >/dev/null 2>&1 && echo "Go is already installed: $$(go version)" || (sudo apt-get update && sudo apt-get install -y golang-go)

test:
	@for lab in $(LABS); do \
		echo "=== testing labs/$$lab ==="; \
		(cd labs/$$lab && go test ./...) || exit 1; \
	done
	@for sys in $(SYSTEMS_GO); do \
		echo "=== testing systems/$$sys ==="; \
		(cd systems/$$sys && go test ./...) || exit 1; \
	done
	@for sys in $(SYSTEMS_PYTHON); do \
		echo "=== testing systems/$$sys ==="; \
		(cd systems/$$sys && python3 -m pytest tests/ -q) || exit 1; \
	done
	@for w in $(WEB); do \
		echo "=== testing web/$$w ==="; \
		(cd web/$$w && test -d node_modules || npm install --silent) && (cd web/$$w && npm test) || exit 1; \
	done

test-race:
	@for lab in $(LABS); do \
		echo "=== testing labs/$$lab with -race ==="; \
		(cd labs/$$lab && go test -race ./...) || exit 1; \
	done
	@for sys in $(SYSTEMS_GO); do \
		echo "=== testing systems/$$sys with -race ==="; \
		(cd systems/$$sys && go test -race ./...) || exit 1; \
	done

lint:
	@for lab in $(LABS); do \
		echo "=== linting labs/$$lab ==="; \
		(cd labs/$$lab && go vet ./... && test -z "$$(gofmt -l .)") || exit 1; \
	done
	@for sys in $(SYSTEMS_GO); do \
		echo "=== linting systems/$$sys ==="; \
		(cd systems/$$sys && go vet ./... && test -z "$$(gofmt -l .)") || exit 1; \
	done
	@for sys in $(SYSTEMS_PYTHON); do \
		echo "=== compile-checking systems/$$sys ==="; \
		(cd systems/$$sys && python3 -m py_compile app/*.py) || exit 1; \
	done
	@for w in $(WEB); do \
		echo "=== type-checking web/$$w ==="; \
		(cd web/$$w && test -d node_modules || npm install --silent) && (cd web/$$w && npx tsc -b) || exit 1; \
	done

bench:
	@if [ -n "$(SYSTEM)" ] && [ -f systems/$(SYSTEM)/go.mod ]; then \
		cd systems/$(SYSTEM) && go test -bench=. -benchmem ./...; \
	elif [ -n "$(SYSTEM)" ] && [ -f systems/$(SYSTEM)/requirements.txt ]; then \
		cd systems/$(SYSTEM) && python3 benchmarks/*.py; \
	elif [ -n "$(LAB)" ]; then \
		cd labs/$(LAB) && go test -bench=. -benchmem ./...; \
	else \
		echo "usage: make bench LAB=rate-limiting  or  make bench SYSTEM=url-shortener"; exit 1; \
	fi

run:
	@if [ -n "$(SYSTEM)" ] && [ -f systems/$(SYSTEM)/go.mod ]; then \
		cd systems/$(SYSTEM) && go run ./cmd/server; \
	elif [ -n "$(SYSTEM)" ] && [ -f systems/$(SYSTEM)/requirements.txt ]; then \
		cd systems/$(SYSTEM) && uvicorn app.main:app --reload; \
	elif [ -n "$(LAB)" ]; then \
		cd labs/$(LAB) && go run ./cmd/...; \
	else \
		echo "usage: make run LAB=rate-limiting  or  make run SYSTEM=url-shortener  or  make run-web"; exit 1; \
	fi

run-web:
	cd web/interactive-lab && (test -d node_modules || npm install) && npm run dev

clean:
	@for lab in $(LABS); do \
		(cd labs/$$lab && go clean); \
	done
	@for sys in $(SYSTEMS_GO); do \
		(cd systems/$$sys && go clean); \
	done
	@for sys in $(SYSTEMS_PYTHON); do \
		find systems/$$sys -name '__pycache__' -type d -exec rm -rf {} + 2>/dev/null; \
		find systems/$$sys -name '*.db' -delete 2>/dev/null; \
	done
	@for w in $(WEB); do \
		rm -rf web/$$w/dist web/$$w/node_modules web/$$w/*.tsbuildinfo; \
	done
	@true
