MODULES=$(shell go list -m)

# Trakt's official OpenAPI document. Only the filtered subset is committed.
SPEC_URL := https://developer.trakt.tv/openapi.json
# Download timeout in seconds: the spec is about 2 MB.
SPEC_FETCH_TIMEOUT := 60
# SPEC_OUT is where spec-fetch writes; SPEC_IN is what generate filters.
SPEC_OUT ?= $(or $(TMPDIR),/tmp)/trakt-openapi.json
SPEC_IN ?= $(SPEC_OUT)

spec-fetch:
	curl -fsSL --max-time $(SPEC_FETCH_TIMEOUT) -o "$(SPEC_OUT)" "$(SPEC_URL)"

# generate filters SPEC_IN (fetched first when missing) down to the endpoints
# in internal/trakt/endpoints.txt, then regenerates the Go client.
generate:
	@if [ ! -f "$(SPEC_IN)" ]; then $(MAKE) spec-fetch SPEC_OUT="$(SPEC_IN)"; fi
	go run ./internal/trakt/gen/filterspec -spec "$(SPEC_IN)" -endpoints internal/trakt/endpoints.txt -out internal/trakt/openapi.filtered.json
	go generate ./internal/trakt/api

.PHONY: spec-fetch generate

build:
	go build -v ./...

plugin-openai:
	mkdir -p dist
	rm -f dist/trakt-openai-plugin.zip
	cd plugin/openai && zip -r -X ../../dist/trakt-openai-plugin.zip . -x '.DS_Store'

test:
	go test -race -v ./...
watch-test:
	reflex -t 50ms -s -- sh -c 'gotest -race -v ./...'

bench:
	go test -benchmem -count 3 -bench ${MODULES} ./...
watch-bench:
	reflex -t 50ms -s -- sh -c 'go test -benchmem -count 3 -bench ${MODULES} ./...'

coverage:
	go test -v -coverprofile=cover.out -covermode=atomic ./...
	go tool cover -html=cover.out -o cover.html

tools:
	go install github.com/cespare/reflex@latest
	go install github.com/rakyll/gotest@latest
	go install github.com/psampaz/go-mod-outdated@latest
	go install github.com/jondot/goweight@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go get -t -u golang.org/x/tools/cmd/cover
	go install github.com/sonatype-nexus-community/nancy@latest
	go install golang.org/x/perf/cmd/benchstat@latest
	go install github.com/cespare/prettybench@latest
	go mod tidy

lint:
	golangci-lint run --timeout 60s --max-same-issues 50 ./...
lint-fix:
	golangci-lint run --timeout 60s --max-same-issues 50 --fix ./...

audit:
	go list -json -m all | nancy sleuth

outdated:
	go list -u -m -json all | go-mod-outdated -update -direct

weight:
	goweight
