FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /prometheus-snapshot-manager ./cmd/prometheus-snapshot-manager

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata apprise
COPY --from=builder /prometheus-snapshot-manager /usr/local/bin/prometheus-snapshot-manager
ENTRYPOINT ["/usr/local/bin/prometheus-snapshot-manager"]
CMD ["run"]
