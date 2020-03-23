OS ?= $(shell go env GOOS)
ARCH ?= $(shell go env GOARCH)
ALL_PLATFORM = linux/amd64,linux/arm/v7,linux/arm64

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
	go test -timeout=1m -v -race -short ./...

.PHONY: fmt
fmt:
	find . -type f -name "*.go" | grep -v "./vendor/*" | xargs gofmt -s -w -l

.PHONY: compile
compile:
	go build -v -o scaleway-csi -ldflags "-X driver.driverVersion=$(TAG)" ./cmd/scaleway-csi

.PHONY: docker-build
docker-build:
	@echo "Building scaleway-csi for ${ARCH}"
	docker build . --platform=linux/$(ARCH) --build-arg ARCH=$(ARCH) --build-arg TAG=$(TAG) --build-arg COMMIT_SHA=$(COMMIT_SHA) --build-arg BUILD_DATE=$(BUILD_DATE) -f Dockerfile -t ${FULL_IMAGE}:${IMAGE_TAG}-$(ARCH)

.PHONY: docker-buildx-all
docker-buildx-all:
	@echo "Making release for tag $(IMAGE_TAG)"
	docker buildx build --build-arg TAG=$(TAG) --build-arg COMMIT_SHA=$(COMMIT_SHA) --build-arg BUILD_DATE=$(BUILD_DATE) --platform=$(ALL_PLATFORM) --push -t $(FULL_IMAGE):$(IMAGE_TAG) .

## Release
.PHONY: release
release: docker-buildx-all

