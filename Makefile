SPHINXENV=doc/.sphinx/venv/bin/activate

.PHONY: default
default: build

# Build targets.
.PHONY: build
build:
	go install -v ./cmd/microclusterctl
	go install -v ./cmd/microcluster

# Snap targets
.PHONY: snaps
snaps:
	rm -f snap *.snap
	ln -s snaps/microcluster-admin snap
	snapcraft
	rm -f snap
	ln -s snaps/microcluster-region snap
	snapcraft
	rm -f snap
	ln -s snaps/microcluster-cell snap
	snapcraft
	rm -f snap

# Testing targets.
.PHONY: check
check: check-static check-unit check-system

.PHONY: check-unit
check-unit:
	go test ./...

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
	./test/lint/no-oneline-assign-and-test.sh
	./test/lint/newline-after-block.sh
	./test/lint/no-short-form-imports.sh
	shellcheck --shell sh test/lint/*.sh

.PHONY: check-system
check-system: build
	cd test && ./main.sh

# Doc targets.
.PHONY: doc
doc:
	@echo "Set up documentation build environment"
	python3 -m venv doc/.sphinx/venv
	. $(SPHINXENV) ; pip install --upgrade -r doc/.sphinx/requirements.txt
	rm -Rf doc/html
	make doc-incremental

.PHONY: doc-incremental
doc-incremental:
	@echo "Build documentation"
	. $(SPHINXENV) ; sphinx-build -c doc/ -b dirhtml doc/ doc/html/ -w doc/.sphinx/warnings.txt

.PHONY: doc-serve
doc-serve:
	cd doc/html; python3 -m http.server 8001

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
	swagger generate spec -c github.com/canonical/microcluster -c github.com/lxc/lxd/lxd/response -o doc/rest-api.yml -w ./api -m
