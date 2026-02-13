# go-cmdexec Makefile
# Go library — no binary targets

.PHONY: all check format check-format format-md check-format-md lint fix test vet coverage coverage-html coverage-report clean clean-coverage

COVERAGE_DIR  := .coverage
COVERAGE_FILE := $(COVERAGE_DIR)/coverage.out
COVERAGE_HTML := $(COVERAGE_DIR)/coverage.html
COVERAGE_THRESHOLD := 80

# ---------- top-level workflows ----------

## all: full local workflow (format, fix, then validate)
all: format fix test vet

## check: CI-friendly, non-mutating checks
check: check-format lint test vet

# ---------- format ----------

## format: auto-format all Go and Markdown files
format: format-md
	@gofumpt -w .

## check-format: fail if any file is not properly formatted
check-format: check-format-md
	@test -z "$$(gofumpt -l .)" || { echo "files not formatted:"; gofumpt -l .; exit 1; }

## format-md: auto-format Markdown files with prettier
format-md:
	@prettier --write "**/*.md"

## check-format-md: fail if any Markdown file is not prettier-formatted
check-format-md:
	@prettier --check "**/*.md"

# ---------- lint / fix ----------

## lint: run golangci-lint (read-only)
lint:
	@golangci-lint run ./...

## fix: auto-fix lint issues (depends on format to avoid concurrent writes)
fix: format
	@golangci-lint run --fix ./...

# ---------- test ----------

## test: run all tests
test:
	@go test ./...

## vet: run go vet
vet:
	@go vet ./...

# ---------- coverage ----------

## coverage: generate coverage profile
coverage: | $(COVERAGE_DIR)
	@go test -coverprofile=$(COVERAGE_FILE) ./...

## coverage-html: open coverage report in browser
coverage-html: coverage
	@go tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "Coverage report: $(COVERAGE_HTML)"

## coverage-report: print coverage summary and enforce threshold
coverage-report: coverage
	@go tool cover -func=$(COVERAGE_FILE) | tail -1
	@total=$$(go tool cover -func=$(COVERAGE_FILE) | tail -1 | awk '{print $$NF}' | tr -d '%'); \
	 if [ "$$(echo "$$total < $(COVERAGE_THRESHOLD)" | bc)" -eq 1 ]; then \
	   echo "FAIL: coverage $$total% < $(COVERAGE_THRESHOLD)% threshold"; exit 1; \
	 fi

$(COVERAGE_DIR):
	@mkdir -p $(COVERAGE_DIR)

# ---------- clean ----------

## clean: remove all generated artifacts
clean: clean-coverage

## clean-coverage: remove coverage artifacts
clean-coverage:
	@rm -rf $(COVERAGE_DIR)
