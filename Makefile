.PHONY: build \
	build-darwin-amd64 build-darwin-arm64 build-linux-amd64 build-linux-arm64 \
	amd arm cross build-cross cross-check \
	module-check shell-check test race vet staticcheck vuln check license-check \
	image-pipeline-test image-pipeline-native-test install-test \
	release-check release-snapshot release-local gr-check gr-snapshot gr-local release-dev

SNAPSHOT_DIST ?= .goreleaser-snapshot

build:
	./packaging/build-dev.sh "$$(go env GOOS)" "$$(go env GOARCH)" bin

build-darwin-amd64:
	./packaging/build-dev.sh darwin amd64 bin/darwin_amd64

build-darwin-arm64:
	./packaging/build-dev.sh darwin arm64 bin/darwin_arm64

build-linux-amd64:
	./packaging/build-dev.sh linux amd64 bin/linux_amd64

build-linux-arm64:
	./packaging/build-dev.sh linux arm64 bin/linux_arm64

amd: build-linux-amd64

arm: build-linux-arm64

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

staticcheck:
	staticcheck ./...

vuln:
	govulncheck ./...

module-check:
	go mod verify
	go mod tidy -diff

shell-check:
	@for script in packaging/*.sh packaging/image-pipeline/*.sh tests/*.sh; do bash -n "$$script"; done

cross: build-darwin-amd64 build-darwin-arm64 build-linux-amd64 build-linux-arm64

build-cross: cross

cross-check:
	GOOS=darwin GOARCH=arm64 go build ./...
	GOOS=darwin GOARCH=amd64 go build ./...
	GOOS=linux GOARCH=amd64 go build ./...
	GOOS=linux GOARCH=arm64 go build ./...

check: module-check shell-check test race vet staticcheck vuln cross-check image-pipeline-test install-test license-check

license-check:
	./packaging/verify-licenses.sh

image-pipeline-test:
	./tests/image-pipeline-test.sh

image-pipeline-native-test:
	FARROW_IMAGE_PIPELINE_NATIVE_REQUIRED=1 ./tests/image-pipeline-native-test.sh

install-test:
	./tests/install-test.sh

release-check:
	./packaging/check-toolchain.sh goreleaser
	goreleaser check

release-snapshot: release-check
	@normalized=$$(printf '%s' "$(SNAPSHOT_DIST)" | tr '[:upper:]' '[:lower:]'); \
	  case "$$normalized" in ""|*/*|.goreleaser-dist|.goreleaser-companion|.goreleaser-licenses) \
	  echo "SNAPSHOT_DIST must be one new directory directly under the repository root" >&2; exit 2 ;; \
	  esac
	@test ! -e "$(SNAPSHOT_DIST)" || { echo "refuse existing snapshot output: $(SNAPSHOT_DIST)" >&2; exit 1; }
	@test ! -e .goreleaser-dist || { echo "refuse existing GoReleaser work output: .goreleaser-dist" >&2; exit 1; }
	@test ! -e .goreleaser-companion -a ! -L .goreleaser-companion || { echo "refuse existing GoReleaser companion stage: .goreleaser-companion" >&2; exit 1; }
	@test ! -e .goreleaser-licenses -a ! -L .goreleaser-licenses || { echo "refuse existing generated license stage: .goreleaser-licenses" >&2; exit 1; }
	@set -e; \
	  source_epoch=$${SOURCE_DATE_EPOCH:-$$(git show -s --format=%ct HEAD)}; \
	  test -n "$$source_epoch" || { echo "cannot resolve SOURCE_DATE_EPOCH" >&2; exit 2; }; \
	  commit=$$(git rev-parse --verify HEAD); \
	  if ! git diff --quiet || ! git diff --cached --quiet || test -n "$$(git status --short --untracked-files=normal)"; then commit=uncommitted; fi; \
	  if build_date=$$(date -u -r "$$source_epoch" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null); then :; \
	  else build_date=$$(date -u -d "@$$source_epoch" +%Y-%m-%dT%H:%M:%SZ); fi; \
	  cleanup_companions() { \
	    ./packaging/goreleaser-companion.sh cleanup darwin amd64 .goreleaser-companion; \
	    ./packaging/goreleaser-companion.sh cleanup darwin arm64 .goreleaser-companion; \
	    ./packaging/goreleaser-companion.sh cleanup linux amd64 .goreleaser-companion; \
	    ./packaging/goreleaser-companion.sh cleanup linux arm64 .goreleaser-companion; \
	    ./packaging/dependency-licenses.sh clean .goreleaser-licenses; \
	    if test -e .goreleaser-dist; then rm -rf -- .goreleaser-dist; fi; \
	  }; \
	  trap cleanup_companions EXIT; \
	  SOURCE_DATE_EPOCH="$$source_epoch" FARROW_COMMIT="$$commit" FARROW_BUILD_DATE="$$build_date" \
	    goreleaser release --snapshot --parallelism 1; \
	  snapshot_version=$$(jq -er '.version | strings | select(length > 0)' .goreleaser-dist/metadata.json); \
	  SOURCE_DATE_EPOCH="$$source_epoch" ./packaging/verify-goreleaser.sh "$$snapshot_version" "$$PWD/.goreleaser-dist"; \
	  ./packaging/verify-linux-packages.sh "$$snapshot_version" "$$commit" "$$source_epoch" "$$PWD/.goreleaser-dist"; \
	  mv .goreleaser-dist "$(SNAPSHOT_DIST)"

release-local:
	@test -n "$(VERSION)" || { echo "VERSION is required" >&2; exit 2; }
	@if test -n "$(OUTPUT)"; then \
	  ./packaging/release-local.sh "$(VERSION)" "$(OUTPUT)"; \
	else \
	  ./packaging/release-local.sh "$(VERSION)"; \
	fi

gr-check: release-check

gr-snapshot: release-snapshot

gr-local: release-local

release-dev:
	@test -n "$(VERSION)" || { echo "VERSION is required" >&2; exit 2; }
	@test -n "$(SOURCE_DATE_EPOCH)" || { echo "SOURCE_DATE_EPOCH is required" >&2; exit 2; }
	SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) ./packaging/build-release.sh $(VERSION) dist/$(VERSION)
	./packaging/verify-release.sh $(VERSION) "$(CURDIR)/dist/$(VERSION)"
