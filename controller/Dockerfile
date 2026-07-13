# syntax=docker/dockerfile:1.7
FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
# staffops-otel-libs is a public org module now — no GOPRIVATE / token needed;
# fetched via the default Go proxy.
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
