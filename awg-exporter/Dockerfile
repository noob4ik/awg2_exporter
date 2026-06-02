# syntax=docker/dockerfile:1

# ---- Build stage ----
FROM golang:1.22-alpine AS builder

WORKDIR /src

COPY exporter/go.mod exporter/go.sum ./
RUN go mod download

COPY exporter/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /awg-exporter .

# ---- Runtime stage ----
FROM alpine:3.19

# docker CLI — нужен для `docker exec`
RUN apk add --no-cache docker-cli

COPY --from=builder /awg-exporter /usr/local/bin/awg-exporter

EXPOSE 9586

ENTRYPOINT ["/usr/local/bin/awg-exporter"]
