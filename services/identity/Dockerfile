# syntax=docker/dockerfile:1
# Tuned for low-RAM builders (Northflank free / shared CI).
# Peak RAM is dominated by rustc link step; we cap jobs and use thin LTO.

FROM rust:1.89-bookworm AS builder
WORKDIR /app

RUN apt-get update \
    && apt-get install -y --no-install-recommends pkg-config libssl-dev \
    && rm -rf /var/lib/apt/lists/*

# Serialize compilation to avoid OOM on ~1–2 GiB builders.
# Increase to 2 if the build host has ≥4 GiB RAM.
ENV CARGO_BUILD_JOBS=1 \
    CARGO_INCREMENTAL=0 \
    CARGO_TERM_COLOR=never \
    RUSTFLAGS="-C debuginfo=0"

COPY Cargo.toml Cargo.lock ./

# Warm dependency cache with a stub crate (layer reused until Cargo.* changes).
RUN mkdir -p src \
    && printf 'fn main() {}\n' > src/main.rs \
    && printf 'pub fn placeholder() {}\n' > src/lib.rs \
    && cargo build --release --locked \
    && rm -rf src \
    && rm -f target/release/identity target/release/identity.d \
    && rm -rf target/release/deps/identity* target/release/.fingerprint/identity*

COPY src ./src
COPY migrations ./migrations

# Touch so cargo rebuilds the binary without invalidating dep cache.
RUN touch src/main.rs src/lib.rs \
    && cargo build --release --locked \
    && strip -s target/release/identity || true

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home --shell /usr/sbin/nologin appuser
WORKDIR /app
COPY --from=builder /app/target/release/identity /app/identity
USER appuser
EXPOSE 8080
ENV HOST=0.0.0.0 \
    PORT=8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD ["curl", "--fail", "--silent", "--show-error", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/app/identity"]
