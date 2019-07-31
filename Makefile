GO              ?= GO15VENDOREXPERIMENT=1 go
GOPATH          := $(firstword $(subst :, ,$(shell $(GO) env GOPATH)))
GOLINTER        ?= $(GOPATH)/bin/gometalinter
pkgs            = $(shell $(GO) list ./... | grep -v /vendor/)
TARGET          ?= s3deploy

PREFIX          ?= $(shell pwd)
BIN_DIR         ?= $(shell pwd)

test:
	@echo ">> running tests"
	@$(GO) test -short $(pkgs)

format:
	@echo ">> formatting code"
	@$(GO) fmt $(pkgs)

gometalinter: $(GOLINTER)
	@echo ">> linting code"
	@$(GOLINTER) --install --update > /dev/null
	@$(GOLINTER) --config=./.gometalinter.json ./...

clean:
	@echo ">> Cleaning up"
	@find . -type f -name '*~' -exec rm -fv {} \;
	@rm -fv $(TARGET)

build:
	@echo ">> building binaries"
	@$(GO) build
