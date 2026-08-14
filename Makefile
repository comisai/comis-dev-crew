SHELL := /bin/sh

.DEFAULT_GOAL := build

.PHONY: docs-check format-check mod-check generate-check vet staticcheck \
	test-architecture test coverage test-race test-conformance test-integration \
	test-fuzz-smoke build cross-build vulncheck license-check secret-check smoke \
	test-live live-baseline live-closeout verify verify-full post-full-check protocol-sync protocol-check

docs-check:
	go run ./tools/checkdocs

format-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }

mod-check:
	go run ./tools/modcheck

protocol-sync:
	@test -n "$(COMIS_ROOT)" || { echo "COMIS_ROOT is required"; exit 1; }
	@test -n "$(COMIS_COMMIT)" || { echo "COMIS_COMMIT is required"; exit 1; }
	go run ./tools/protocolsync -source-root "$(COMIS_ROOT)" -source-commit "$(COMIS_COMMIT)" -destination-root protocol/comis

protocol-check:
	go run ./tools/protocolcheck -root protocol/comis -generated internal/comiswire/protocol.gen.go

generate-check: protocol-check
	go generate ./...

vet:
	go vet ./...

staticcheck:
	go tool staticcheck ./...

test-architecture:
	go test -mod=readonly -count=1 -timeout=5m ./test/architecture/...

test:
	go test -mod=readonly -count=1 -shuffle=on -timeout=10m ./...

coverage:
	go test -mod=readonly -count=1 -covermode=atomic -coverpkg=./internal/... -coverprofile=coverage.out ./...
	go run ./tools/checkcoverage -profile coverage.out

test-race:
	go test -mod=readonly -race -count=1 -timeout=20m ./...

test-conformance:
	go test -mod=readonly -count=1 -timeout=10m ./test/conformance/...

test-integration:
	go test -mod=readonly -tags=integration -p=1 -count=1 -timeout=30m ./test/integration/...

test-fuzz-smoke:
	go test -mod=readonly -run '^$$' -fuzz '^FuzzCommandArguments$$' -fuzztime=1s ./test/fuzz

build:
	go build -trimpath ./cmd/...

cross-build:
	@set -eu; \
	for platform in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do \
		goos=$${platform%/*}; goarch=$${platform#*/}; \
		echo "building GOOS=$$goos GOARCH=$$goarch"; \
		CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch go build -trimpath ./cmd/...; \
	done

vulncheck:
	go tool govulncheck ./...

license-check:
	go run ./tools/checklicenses

secret-check:
	go run ./tools/checksecrets

smoke:
	go run ./tools/smoke

post-full-check:
	go run ./tools/modcheck

test-live:
	go test -mod=readonly -tags=live -count=1 -timeout=2h ./test/live/...

live-baseline:
	@test -n "$(DEVCREW_LIVE_MANIFEST)" || { echo "DEVCREW_LIVE_MANIFEST is required"; exit 1; }
	@test -n "$(DEVCREW_LIVE_RESOURCE_BASELINE)" || { echo "DEVCREW_LIVE_RESOURCE_BASELINE is required"; exit 1; }
	go run ./tools/livebaseline --manifest "$(DEVCREW_LIVE_MANIFEST)" --output "$(DEVCREW_LIVE_RESOURCE_BASELINE)"

live-closeout:
	@test -n "$(DEVCREW_LIVE_MANIFEST)" || { echo "DEVCREW_LIVE_MANIFEST is required"; exit 1; }
	@test -n "$(DEVCREW_LIVE_EVIDENCE_ROOT)" || { echo "DEVCREW_LIVE_EVIDENCE_ROOT is required"; exit 1; }
	@test -n "$(DEVCREW_LIVE_RESOURCE_BASELINE)" || { echo "DEVCREW_LIVE_RESOURCE_BASELINE is required"; exit 1; }
	go run ./tools/livecloseout --manifest "$(DEVCREW_LIVE_MANIFEST)" --evidence-root "$(DEVCREW_LIVE_EVIDENCE_ROOT)" --resource-baseline "$(DEVCREW_LIVE_RESOURCE_BASELINE)"

verify: docs-check format-check mod-check generate-check vet staticcheck test-architecture test coverage test-race test-conformance build

verify-full: verify test-integration test-fuzz-smoke cross-build vulncheck license-check secret-check smoke post-full-check
