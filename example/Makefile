VERSION=$(shell git describe --always --dirty --abbrev=10)
LDFLAGS="-X github.com/canonical/microcluster/v2/example/version.version=$(VERSION)"

.PHONY: default
default: build

# Build targets.
.PHONY: build
build:
	go install -v \
		-ldflags $(LDFLAGS) \
		./cmd/microctl
	go install -v \
		-ldflags $(LDFLAGS) \
		./cmd/microd

# Testing targets.
.PHONY: check
check: check-static check-unit check-system

.PHONY: check-unit
check-unit:
	go test ./...

.PHONY: check-system
check-system: build
	./test/main.sh

.PHONY: check-static
check-static:
ifeq ($(shell command -v golangci-lint 2> /dev/null),)
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin
endif
	golangci-lint run --timeout 5m

# Update targets.
.PHONY: update-gomod
update-gomod:
	go get -u ./...
	go mod tidy

# Update lxd-generate generated database helpers.
.PHONY: update-schema
update-schema:
	go generate ./...
	gofmt -s -w ./database/
	goimports -w ./database/
	@echo "Code generation completed"

