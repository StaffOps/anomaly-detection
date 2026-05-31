FROM golang:1.22-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -o /bin/controller ./cmd/controller/
RUN CGO_ENABLED=0 go build -o /bin/worker ./cmd/worker/

# Controller image
FROM alpine:3.20 AS controller
RUN apk add --no-cache ca-certificates
COPY --from=builder /bin/controller /usr/local/bin/controller
ENTRYPOINT ["controller"]

# Worker image
FROM alpine:3.20 AS worker
RUN apk add --no-cache ca-certificates
COPY --from=builder /bin/worker /usr/local/bin/worker
ENTRYPOINT ["worker"]
