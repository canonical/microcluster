GOMIN=1.25.4
GOPATH ?= $(shell go env GOPATH)
DQLITE_PATH=$(GOPATH)/deps/dqlite
DQLITE_BRANCH=main

.PHONY: default
default: update-schema

# Build dependencies
.PHONY: deps
deps:
	# dqlite (+raft)
	@if [ ! -e "$(DQLITE_PATH)" ]; then \
		echo "Retrieving dqlite from ${DQLITE_BRANCH} branch"; \
		git clone --depth=1 --branch "${DQLITE_BRANCH}" "https://github.com/canonical/dqlite" "$(DQLITE_PATH)"; \
	elif [ -e "$(DQLITE_PATH)/.git" ]; then \
		echo "Updating existing dqlite branch"; \
		cd "$(DQLITE_PATH)"; git pull; \
	fi

	cd "$(DQLITE_PATH)" && \
		autoreconf -i && \
		./configure --enable-build-raft && \
		make

# Testing targets.
.PHONY: check
check: check-static check-unit check-system

.PHONY: check-unit
check-unit:
ifeq "$(GOCOVERDIR)" ""
	go test ./...
else
	go test ./... -cover -test.gocoverdir="${GOCOVERDIR}"
endif

.PHONY: check-system
check-system:
	true

.PHONY: check-static
check-static:
ifeq ($(shell command -v golangci-lint 2> /dev/null),)
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin
endif
	golangci-lint run --timeout 5m
	run-parts --verbose --exit-on-error --regex '.sh' test/lint

# Update targets.
.PHONY: update-gomod
update-gomod:
	go get -t -v -u ./...

	go get github.com/canonical/lxd@stable-5.21 # Stay on v2 dqlite and specific LXD LTS client from stable-5.21 branch
	go get github.com/olekukonko/tablewriter@v0.0.5 # Due to breaking API in later versions

	go mod tidy -go=$(GOMIN)

	# Use the bundled toolchain that meets the minimum go version
	go get toolchain@none
