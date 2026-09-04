# syntax=docker/dockerfile:1.7

FROM golang:1.23-alpine AS builder

RUN apk add --no-cache build-base

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build \
      -trimpath \
      -ldflags="-s -w -linkmode external -extldflags '-static'" \
      -o /out/ipasd ./cmd/ipasd

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 ipasd \
    && adduser -S -D -H -u 10001 -G ipasd ipasd \
    && mkdir -p /app/upload \
    && chown -R ipasd:ipasd /app

WORKDIR /app
COPY --from=builder --chown=ipasd:ipasd /out/ipasd /usr/local/bin/ipasd
COPY --chown=ipasd:ipasd docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod 0555 /usr/local/bin/ipasd /usr/local/bin/docker-entrypoint.sh

USER ipasd
EXPOSE 8080
VOLUME ["/app/upload"]

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
