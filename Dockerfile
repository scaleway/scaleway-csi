FROM golang:1.22-alpine AS builder

RUN apk update && apk add --no-cache git ca-certificates && update-ca-certificates

WORKDIR /go/src/github.com/scaleway/scaleway-csi

COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

COPY cmd/ cmd/
COPY pkg/ pkg/

ARG TAG
ARG COMMIT_SHA
ARG BUILD_DATE
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -a -ldflags "-w -s -X github.com/scaleway/scaleway-csi/driver.driverVersion=${TAG} -X github.com/scaleway/scaleway-csi/driver.buildDate=${BUILD_DATE} -X github.com/scaleway/scaleway-csi/driver.gitCommit=${COMMIT_SHA} " -o scaleway-csi ./cmd/scaleway-csi

FROM alpine:3.15
RUN apk update && apk add --no-cache e2fsprogs e2fsprogs-extra xfsprogs xfsprogs-extra cryptsetup ca-certificates blkid && update-ca-certificates
WORKDIR /
COPY --from=builder /go/src/github.com/scaleway/scaleway-csi/scaleway-csi .
ENTRYPOINT ["/scaleway-csi"]
