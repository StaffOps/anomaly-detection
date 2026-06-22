# syntax=docker/dockerfile:1.7
FROM golang:1.22-alpine AS builder

# git: needed to fetch the private staffops-otel-libs module over HTTPS.
RUN apk add --no-cache git

# Private module: bypass the public proxy/checksum DB. Auth uses an HTTPS
# token mounted as a BuildKit secret (id=github_token) — never baked into a
# layer. Same DOCS_DEPLOY_TOKEN pattern used across the org's repos.
ENV GOPRIVATE=github.com/karlipegomes/*

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=secret,id=github_token \
    git config --global credential.helper store \
 && printf "https://x-access-token:%s@github.com\n" "$(cat /run/secrets/github_token)" > ~/.git-credentials \
 && go mod download \
 && rm -f ~/.git-credentials

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
