GO ?= go
GOLANGCI_LINT ?= golangci-lint
GORELEASER ?= goreleaser
DOCKER ?= docker

GO_PACKAGES ?= ./...
GO_COVERAGE_FILE ?= cover.out
VERSION ?= devel
REVISION ?= $(shell git rev-parse --verify HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= unknown
LDFLAGS := -s -w \
	-X main.cliVersion=$(VERSION) \
	-X main.cliRevision=$(REVISION) \
	-X main.cliBuildDate=$(BUILD_DATE)

IMAGE_CONTEXT ?= .
DOCKERFILE ?= Dockerfile
IMAGE_REF ?= local/hami-cli:dev
IMAGE_PLATFORM ?= linux/amd64
IMAGE_SOURCE ?= local
IMAGE_REVISION ?= $(REVISION)

.PHONY: build ci fmt fmt-check vet lint test verify-build verify release-check \
	build-image build-image-amd64 build-image-arm64 package-release

build:
	mkdir -p bin
	GOWORK=off CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/hami-cli .

fmt:
	$(GOLANGCI_LINT) fmt --config .golangci.yml

fmt-check:
	$(GOLANGCI_LINT) fmt --diff --config .golangci.yml

vet:
	GOWORK=off $(GO) vet $(GO_PACKAGES)

lint:
	GOWORK=off $(GOLANGCI_LINT) run --config .golangci.yml $(GO_PACKAGES)

test:
	GOWORK=off $(GO) test -race -covermode=atomic -coverprofile="$(GO_COVERAGE_FILE)" $(GO_PACKAGES)
	test -s "$(GO_COVERAGE_FILE)"

verify-build:
	GOWORK=off CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o /dev/null .

ci: fmt-check vet lint test verify-build
	git diff --check

verify: test vet
	git diff --check

define build_image
	test -n "$${HAMI_ENTERPRISE_REPO_TOKEN}"
	$(DOCKER) buildx build \
		--file "$(DOCKERFILE)" \
		--platform "$(1)" \
		--tag "$(2)" \
		--label "org.opencontainers.image.source=$(IMAGE_SOURCE)" \
		--label "org.opencontainers.image.revision=$(IMAGE_REVISION)" \
		--build-arg "VERSION=$(VERSION)" \
		--build-arg "REVISION=$(IMAGE_REVISION)" \
		--build-arg "BUILD_DATE=$(BUILD_DATE)" \
		--secret id=github_token,env=HAMI_ENTERPRISE_REPO_TOKEN \
		--load \
		"$(IMAGE_CONTEXT)"
endef

build-image:
	$(call build_image,$(IMAGE_PLATFORM),$(IMAGE_REF))

build-image-amd64:
	$(call build_image,linux/amd64,$(IMAGE_REF)-amd64)

build-image-arm64:
	$(call build_image,linux/arm64,$(IMAGE_REF)-arm64)

package-release:
	RELEASE_VERSION="$(VERSION)" $(GORELEASER) release --snapshot --clean

release-check:
	$(GORELEASER) check
