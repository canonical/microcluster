GOMIN=1.22.0

.PHONY: default
default: update-schema

# Testing targets.
.PHONY: check
check: check-static check-unit check-system

.PHONY: check-unit
check-unit:
	go test ./...

.PHONY: check-system
check-system:
	true

.PHONY: check-static
check-static:
ifeq ($(shell command -v golangci-lint 2> /dev/null),)
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin
endif
	golangci-lint run --timeout 5m

# Update targets.
.PHONY: update-gomod
update-gomod:
	go get -t -v -d -u ./...
	go mod tidy -go=$(GOMIN)
	# Eliminate toolchain directive in go.mod
	go get toolchain@none

# Update lxd-generate generated database helpers.
.PHONY: update-schema
update-schema:
	go generate ./cluster/...
	gofmt -s -w ./cluster/
	goimports -w ./cluster/
	@echo "Code generation completed"

