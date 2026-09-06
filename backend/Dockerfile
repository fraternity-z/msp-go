# syntax=docker/dockerfile:1

FROM golang:1.25.13-alpine AS builder

WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/msp-api ./cmd/api \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/msp-migrate ./cmd/migrate \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/msp-vector-worker ./cmd/vector-worker

FROM alpine:3.22

WORKDIR /app

RUN apk add --no-cache ca-certificates poppler-utils \
    && adduser -D -u 1000 appuser && mkdir -p /app/uploads && chown -R appuser:appuser /app

COPY --from=builder /out/msp-api /usr/local/bin/msp-api
COPY --from=builder /out/msp-migrate /usr/local/bin/msp-migrate
COPY --from=builder /out/msp-vector-worker /usr/local/bin/msp-vector-worker

USER appuser

EXPOSE 8000

HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost:8000/health || exit 1

CMD ["msp-api"]
