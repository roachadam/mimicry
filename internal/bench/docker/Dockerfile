# One image for every benchmark container: the Go server, the proxy, the
# setup step and the client runner (bench, the layer 1-2 test binary and
# benchstat). Built from the repository root, because the bench module
# replaces github.com/roachadam/mimicry with ../../ (compose files set the
# context).
FROM golang:1.27.3-trixie AS build

WORKDIR /src
ENV GOWORK=off CGO_ENABLED=0

# Module downloads first, so source edits keep this layer.
COPY go.mod go.sum ./
COPY internal/bench/go.mod internal/bench/go.sum internal/bench/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd internal/bench && go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    cd internal/bench && \
    go build -trimpath -o /out/ ./cmd/server ./cmd/proxy ./cmd/setup ./cmd/bench && \
    go test -c -trimpath -o /out/bench.test . && \
    go build -trimpath -o /out/benchstat golang.org/x/perf/cmd/benchstat

FROM debian:trixie-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends iproute2 ethtool ca-certificates && \
    rm -rf /var/lib/apt/lists/*

COPY --from=build /out/ /usr/local/bin/
COPY internal/bench/docker/proxy-entrypoint.sh /usr/local/bin/proxy-entrypoint.sh
