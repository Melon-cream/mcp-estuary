FROM golang:1.26 AS builder

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/mcpe ./cmd/mcpe

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl nodejs npm docker.io \
    && curl -LsSf https://astral.sh/uv/install.sh | env UV_UNMANAGED_INSTALL=/usr/local/bin UV_NO_MODIFY_PATH=1 sh \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/mcpe /usr/local/bin/mcpe

WORKDIR /workspace
EXPOSE 8080

ENV INSTALL_CONCURRENCY=2
CMD ["mcpe", "serve", "--config", "/workspace/mcpe.json", "--listen", "0.0.0.0:8080"]
