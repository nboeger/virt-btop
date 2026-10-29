GO   ?= go
BIN  ?= bin/virt-btop
TAGS ?= libvirt_dlopen
VERSION ?= $(shell git describe --tags --always 2>/dev/null | sed "s/^v//")
LDFLAGS = $(if $(VERSION),-ldflags "-X main.version=$(VERSION)")

.PHONY: build build-linked run test vet fmt install clean

build:
	CGO_ENABLED=1 $(GO) build -tags $(TAGS) $(LDFLAGS) -o $(BIN) ./cmd/virt-btop

# For hosts with libvirt-dev headers installed (links libvirt at build time).
build-linked:
	CGO_ENABLED=1 $(GO) build -o $(BIN) ./cmd/virt-btop

run: build
	./$(BIN)

test:
	CGO_ENABLED=1 $(GO) test -tags $(TAGS) ./...

vet:
	CGO_ENABLED=1 $(GO) vet -tags $(TAGS) ./...

fmt:
	gofmt -w cmd internal

install:
	CGO_ENABLED=1 $(GO) install -tags $(TAGS) ./cmd/virt-btop

clean:
	rm -rf bin
