# syntax=docker/dockerfile:1.7
FROM golang:1.26.11-alpine AS builder

# staffops-otel-libs had its history rewritten (git filter-repo, 2026-10-28): the
# go/v0.2.x tags now point at rewritten commits, sum.golang.org keeps the old hash
# and proxy.golang.org no longer serves the module. Fetch it directly from GitHub
# (needs git, builder stage only) and skip the checksum DB; go.sum pins the hash of
# the current tag contents. Remove once we move to otel-libs 0.3.0
# (module path github.com/staffops/otel-libs/go).
RUN apk add --no-cache git
ENV GOPRIVATE=github.com/staffops/* \
    GONOSUMDB=github.com/staffops/*

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -o /bin/controller ./cmd/controller/
RUN CGO_ENABLED=0 go build -o /bin/worker ./cmd/worker/

# Controller image
FROM alpine:3.20 AS controller
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /bin/controller /usr/local/bin/controller
# Run as nobody (uid 65534) — satisfies runAsNonRoot Kyverno policy
USER 65534:65534
ENTRYPOINT ["controller"]

# Worker image
FROM alpine:3.20 AS worker
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /bin/worker /usr/local/bin/worker
USER 65534:65534
ENTRYPOINT ["worker"]
