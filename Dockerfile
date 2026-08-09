FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/binance-monitor \
    ./cmd/binance-monitor

FROM alpine:3.23

WORKDIR /app

RUN addgroup -S reporter \
    && adduser -S -G reporter reporter \
    && mkdir -p /app/state \
    && chown reporter:reporter /app/state

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/binance-monitor /usr/local/bin/binance-monitor

USER reporter

CMD ["binance-monitor", "--daemon"]
