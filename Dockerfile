FROM golang:1.25-alpine AS build

RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o /out/ai-gateway-metering-proxy .

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata sqlite

RUN addgroup -g 1000 appuser && adduser -D -u 1000 -G appuser appuser

COPY --from=build /out/ai-gateway-metering-proxy /usr/local/bin/ai-gateway-metering-proxy

USER appuser
EXPOSE 8320

# Lightweight process liveness check using busybox wget already present in Alpine.
# Prefer /healthz so orchestrators do not thrash restarts on external dependency flaps.
ENV METERING_PROXY_HEALTH_URL=http://127.0.0.1:8320/healthz
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 \
  CMD wget -q -O /dev/null "$METERING_PROXY_HEALTH_URL" || exit 1

ENTRYPOINT ["ai-gateway-metering-proxy"]
