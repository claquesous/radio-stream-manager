# Build context must be the workspace root so we can access radio/ClaqRadio.pm.
# In docker-compose.yml: context: . / dockerfile: radio-stream-manager/Dockerfile

# ── Stage 1: Build ices0 from source ──────────────────────────
# ices0 (the Perl-scriptable MP3 streaming variant) was dropped from Debian
# repos, so we compile it ourselves. The last stable release is 0.4 (2012).
FROM debian:bookworm-slim AS ices-build

RUN apt-get update -qq && \
    apt-get install -y --no-install-recommends \
      build-essential \
      autoconf \
      automake \
      libtool \
      libshout3-dev \
      libmp3lame-dev \
      libxml2-dev \
      libperl-dev \
      curl \
      ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Download ices-0.4 tarball and build with Perl + lame support.
# -Wno-implicit-function-declaration silences warnings that would be errors on
# newer GCC versions (ices0 predates C99 strict mode).
RUN curl -fsSL https://downloads.xiph.org/releases/ices/ices-0.4.tar.gz \
      | tar -xz -C /tmp && \
    cd /tmp/ices-0.4 && \
    ./configure \
      --with-perl \
      --with-lame \
      --without-vorbis \
      --prefix=/usr \
      CFLAGS="-Wno-implicit-function-declaration" && \
    make -j"$(nproc)" && \
    make install

# ── Stage 2: Build Go binary ───────────────────────────────────
FROM golang:1.24-bookworm AS go-build

WORKDIR /src

COPY radio-stream-manager/go.mod radio-stream-manager/go.sum ./
RUN go mod download

COPY radio-stream-manager/ .

RUN CGO_ENABLED=0 GOOS=linux go build \
      -ldflags="-s -w" \
      -o /app/radio-stream-manager \
      ./cmd/radio-stream-manager

# ── Stage 3: Runtime ───────────────────────────────────────────
FROM debian:bookworm-slim AS runtime

# Runtime shared libraries required by the ices binary
RUN apt-get update -qq && \
    apt-get install -y --no-install-recommends \
      libshout3 \
      libxml2 \
      libmp3lame0 \
      perl \
      libjson-perl \
      curl \
      ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -u 1000 streamer && \
    mkdir -p \
      /app \
      /var/lib/radio-stream-manager/ices-configs/modules \
      /var/log/radio-stream-manager && \
    chown -R streamer:streamer \
      /var/lib/radio-stream-manager \
      /var/log/radio-stream-manager

# ices binary from the ices-build stage
COPY --from=ices-build /usr/bin/ices /usr/bin/ices

# Go binary and its co-located template (generator.go: filepath.Dir(os.Args[0]))
COPY --from=go-build /app/radio-stream-manager /app/radio-stream-manager
COPY radio-stream-manager/ices-template.xml    /app/ices-template.xml

# Perl callback — placed where ices looks by default (/usr/etc/modules)
RUN mkdir -p /usr/etc/modules
COPY radio/ClaqRadio.pm /usr/etc/modules/ClaqRadio.pm

USER streamer

CMD ["/app/radio-stream-manager"]
