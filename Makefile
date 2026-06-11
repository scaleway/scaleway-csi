OS ?= $(shell go env GOOS)
ARCH ?= $(shell go env GOARCH)
ARCHS ?= amd64 arm64

BUILD_DATE ?= $(shell date -Is)

GOPATH ?= $(GOPATH)

REGISTRY ?= scaleway
IMAGE ?= scaleway-csi
FULL_IMAGE ?= $(REGISTRY)/$(IMAGE)

TAG ?= $(shell git rev-parse HEAD)
IMAGE_TAG ?= $(shell git rev-parse HEAD)
COMMIT_SHA ?= $(shell git rev-parse HEAD)

DOCKER_CLI_EXPERIMENTAL ?= enabled

.PHONY: default
default: test compile

.PHONY: clean
clean:
	go clean -i -x ./...

.PHONY: test
test:
	go test -timeout=1m -v -race -short `go list ./... | grep -v /test`

.PHONY: test-sanity
test-sanity:
	go test -count=1 -v -timeout 10m github.com/scaleway/scaleway-csi/test/sanity

.PHONY: fmt
fmt:
	find . -type f -name "*.go" | grep -v "./vendor/*" | xargs gofmt -s -w -l

.PHONY: compile
compile:
	go build -v -o scaleway-csi -ldflags "-X github.com/scaleway/scaleway-csi/pkg/driver.driverVersion=$(TAG) -X github.com/scaleway/scaleway-csi/pkg/driver.buildDate=$(BUILD_DATE) -X github.com/scaleway/scaleway-csi/pkg/driver.gitCommit=$(COMMIT_SHA)" ./cmd/scaleway-csi

.PHONY: docker-build
docker-build:
	@echo "Building scaleway-csi for ${ARCH}"
	docker build . --platform=linux/$(ARCH) --build-arg ARCH=$(ARCH) --build-arg TAG=$(TAG) --build-arg COMMIT_SHA=$(COMMIT_SHA) --build-arg BUILD_DATE=$(BUILD_DATE) -f Dockerfile -t ${FULL_IMAGE}:${IMAGE_TAG}-$(ARCH)

.PHONY: docker-push-arch
docker-push-arch:
	@echo "Building and pushing scaleway-csi for $(ARCH)"
	mkdir -p digests
	docker buildx build . --platform=linux/$(ARCH) --build-arg TAG=$(TAG) --build-arg COMMIT_SHA=$(COMMIT_SHA) --build-arg BUILD_DATE=$(BUILD_DATE) \
		--output type=image,name=$(FULL_IMAGE),push=true,push-by-digest=true,name-canonical=true \
		--metadata-file digests/$(ARCH).json
	jq -r '."containerimage.digest"' digests/$(ARCH).json > digests/$(ARCH)

.PHONY: docker-manifest
docker-manifest:
	@echo "Creating manifest $(FULL_IMAGE):$(IMAGE_TAG) from $(foreach arch,$(ARCHS),digests/$(arch))"
	docker buildx imagetools create -t $(FULL_IMAGE):$(IMAGE_TAG) $(foreach arch,$(ARCHS),$(FULL_IMAGE)@$(shell cat digests/$(arch)))

.PHONY: docker-push-all
docker-push-all:
	@for arch in $(ARCHS); do $(MAKE) docker-push-arch ARCH=$$arch; done
	$(MAKE) docker-manifest

## Release
.PHONY: release
release: docker-push-all

