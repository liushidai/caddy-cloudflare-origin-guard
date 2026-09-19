FROM golang:1.27.1-bookworm AS builder

WORKDIR /src

RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@v0.4.7

COPY . .

RUN xcaddy build v2.11.4 \
    --with github.com/liushidai/caddy-cloudflare-origin-guard=.

FROM caddy:2.11.4

COPY --from=builder /src/caddy /usr/bin/caddy
