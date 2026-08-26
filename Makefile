.PHONY: build test race vet staticcheck vuln cross check license-check profile-contract pigsty-source-test wrapper-test image-pipeline-test release-dev

build:
	install -d -m 0755 bin
	install -m 0755 packaging/pigsty/vm bin/pigsty-vm
	go build -o bin/farrow-hosts-helper ./cmd/farrow-hosts-helper
	helper_sha=$$(shasum -a 256 bin/farrow-hosts-helper | awk '{print $$1}'); \
	  go build -ldflags "-X github.com/pgsty/farrow/internal/hostconfig.ExpectedHelperSHA256=$$helper_sha" -o bin/farrow ./cmd/farrow
	go build -o bin/farrow-m0 ./cmd/farrow-m0
	go build -o bin/farrow-net-stage ./cmd/farrow-net-stage
	go build -o bin/farrow-linux-net-stage ./cmd/farrow-linux-net-stage
	go build -o bin/farrow-private-m0 ./cmd/farrow-private-m0
	go build -o bin/farrow-private-fd-m0 ./cmd/farrow-private-fd-m0

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

cross:
	GOOS=darwin GOARCH=arm64 go build ./...
	GOOS=darwin GOARCH=amd64 go build ./...
	GOOS=linux GOARCH=amd64 go build ./...
	GOOS=linux GOARCH=arm64 go build ./...

check: test race vet staticcheck vuln cross profile-contract wrapper-test image-pipeline-test license-check

license-check:
	./packaging/verify-licenses.sh

profile-contract:
	go test ./internal/profile

pigsty-source-test:
	@test -n "$(PIGSTY_SOURCE)" || { echo "PIGSTY_SOURCE is required" >&2; exit 2; }
	PIGSTY_SOURCE="$(PIGSTY_SOURCE)" ./tests/pigsty-source-integration-test.sh

wrapper-test:
	./tests/pigsty-wrapper-test.sh

image-pipeline-test:
	./tests/image-pipeline-test.sh
	./tests/image-pipeline-native-test.sh

release-dev:
	@test -n "$(VERSION)" || { echo "VERSION is required" >&2; exit 2; }
	@test -n "$(SOURCE_DATE_EPOCH)" || { echo "SOURCE_DATE_EPOCH is required" >&2; exit 2; }
	SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) ./packaging/build-release.sh $(VERSION) dist/$(VERSION)
	./packaging/verify-release.sh $(VERSION) dist/$(VERSION)
