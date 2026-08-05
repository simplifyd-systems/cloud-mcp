FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

RUN apk add --no-cache ca-certificates && update-ca-certificates

WORKDIR /build

ENV GO111MODULE=on \
    CGO_ENABLED=0

# cloud-go-sdk is a published, public module, so the build needs nothing from
# the rest of the monorepo. Build context is this directory:
#   docker build -f Dockerfile .
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -ldflags="-s -w -X main.serverVersion=${VERSION}" -o cloud-mcp .

FROM scratch AS final

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /build/cloud-mcp /go/bin/cloud-mcp

# The image is only useful in HTTP mode: a stdio server is launched as a
# subprocess by a local client, not run as a container.
ENV MCP_TRANSPORT=http \
    MCP_ADDR=:8080

EXPOSE 8080

ENTRYPOINT ["/go/bin/cloud-mcp"]
