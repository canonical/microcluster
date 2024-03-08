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
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
endif
ifeq ($(shell command -v shellcheck 2> /dev/null),)
	echo "Please install shellcheck"
	exit 1
endif
ifeq ($(shell command -v revive 2> /dev/null),)
	go install github.com/mgechev/revive@latest
endif
	golangci-lint run --timeout 5m
	revive -set_exit_status ./...

# Update targets.
.PHONY: update-gomod
update-gomod:
	go get -u ./...
	go mod tidy

.PHONY: update-api
update-api:
ifeq ($(shell command -v swagger 2> /dev/null),)
	go install github.com/go-swagger/go-swagger/cmd/swagger@latest
endif
	swagger generate spec -o doc/rest-api.yaml -c github.com/canonical/microcluster -x github.com/canonical/lxd/shared -c github.com/canonical/lxd/lxd/response -w internal/rest/resources -m

# Update lxd-generate generated database helpers.
.PHONY: update-schema
update-schema:
	go generate ./cluster/...
	gofmt -s -w ./cluster/
	goimports -w ./cluster/
	@echo "Code generation completed"

