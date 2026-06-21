# syntax=docker/dockerfile:1.7
FROM golang:1.22-alpine AS builder

# git + openssh: needed to fetch the private staffops-otel-libs module.
# The SSH key is forwarded from the build host's agent (--mount=type=ssh)
# and is NEVER baked into an image layer. Same mechanism works in CI, where
# the runner loads the deploy key into ssh-agent before the build.
RUN apk add --no-cache git openssh-client

# Private module: bypass the public proxy/checksum DB and force SSH transport.
ENV GOPRIVATE=github.com/karlipegomes/*
RUN git config --global url."git@github.com:".insteadOf "https://github.com/" \
 && mkdir -p -m 0700 /root/.ssh \
 && ssh-keyscan github.com >> /root/.ssh/known_hosts 2>/dev/null

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=ssh go mod download

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
