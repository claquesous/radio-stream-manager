# Build context is the workspace root, as in docker-compose.yml:
# context: . / dockerfile: radio-stream-manager/Dockerfile

# ── Stage 1: Build Go binary ───────────────────────────────────
FROM golang:1.24-trixie AS go-build

WORKDIR /src

COPY radio-stream-manager/go.mod radio-stream-manager/go.sum ./
RUN go mod download

COPY radio-stream-manager/ .

RUN CGO_ENABLED=0 GOOS=linux go build \
      -ldflags="-s -w" \
      -o /app/radio-stream-manager \
      ./cmd/radio-stream-manager

# ── Stage 2: Runtime ───────────────────────────────────────────
# trixie because Liquidsoap's upstream .debs start there.
FROM debian:trixie-slim AS runtime

RUN apt-get update -qq && \
    apt-get install -y --no-install-recommends \
      curl \
      ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Liquidsoap from upstream: Debian's package is older. apt pulls in its
# dependencies (ffmpeg, lame, ...).
ARG LIQUIDSOAP_VERSION=2.4.6
RUN deb="liquidsoap_${LIQUIDSOAP_VERSION}-debian-trixie-ocaml4.14.2-3_$(dpkg --print-architecture).deb" && \
    curl -fsSL -o "/tmp/$deb" \
      "https://github.com/savonet/liquidsoap/releases/download/v${LIQUIDSOAP_VERSION}/$deb" && \
    apt-get update -qq && \
    apt-get install -y --no-install-recommends "/tmp/$deb" && \
    rm -rf "/tmp/$deb" /var/lib/apt/lists/*

RUN useradd -m -u 1000 streamer && \
    mkdir -p /app

# Go binary and its co-located Liquidsoap script
# (resolved via filepath.Dir(os.Args[0]))
COPY --from=go-build /app/radio-stream-manager /app/radio-stream-manager
COPY radio-stream-manager/claqradio.liq        /app/claqradio.liq

USER streamer

CMD ["/app/radio-stream-manager"]
