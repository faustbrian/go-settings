GO ?= go
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
STATICCHECK ?= go run honnef.co/go/tools/cmd/staticcheck@v0.7.0
GOVULNCHECK ?= go run golang.org/x/vuln/cmd/govulncheck@v1.6.0
ACTIONLINT ?= go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
FUZZ_TIME ?= 2s
BENCH_TIME ?= 100x

.PHONY: benchmark check coverage docs examples format format-check fuzz \
	integration lint mutation race staticcheck test tidy-check vet vuln workflows

format:
	gofmt -w .
format-check:
	test -z "$$(gofmt -l .)"
tidy-check:
	$(GO) mod tidy -diff
test:
	$(GO) test ./...
race:
	$(GO) test -race ./... -count=1
coverage:
	bash scripts/check-coverage.sh
vet:
	$(GO) vet ./...
lint:
	$(GOLANGCI_LINT) run --timeout=5m ./...
staticcheck:
	$(STATICCHECK) ./...
vuln:
	$(GOVULNCHECK) ./...
fuzz:
	bash scripts/check-fuzz.sh "$(FUZZ_TIME)"
mutation:
	bash scripts/check-mutation.sh
integration:
	test -n "$(POSTGRES_URL)"
	test -n "$(VALKEY_ADDR)"
	$(GO) test -race ./postgres ./valkey -count=1 -v
examples:
	$(GO) test . -run '^Example' -count=1
docs:
	bash scripts/check-docs.sh
workflows:
	$(ACTIONLINT) .github/workflows/*.yml
benchmark:
	$(GO) test ./memory ./valkey -run '^$$' -bench=. -benchmem \
		-benchtime="$(BENCH_TIME)"
check: tidy-check format-check vet test race coverage fuzz examples docs \
	staticcheck lint vuln benchmark workflows
