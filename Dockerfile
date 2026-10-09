# Builder: Debian + mise toolchain (go, sqlc, templ, tailwind), full build.
FROM debian:trixie-slim AS builder

RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl git \
  && rm -rf /var/lib/apt/lists/*

# Install mise (jdx.dev) for the pinned toolchain in mise.toml.
RUN curl -fsSL https://mise.run | sh
ENV PATH="/root/.local/bin:${PATH}"
ENV MISE_TRUSTED_CONFIG_PATHS="/src"

WORKDIR /src

# Toolchain + module manifests first: cached unless versions change.
COPY mise.toml go.mod go.sum ./
RUN mise install

# Sources, then the prod build (sqlc + templ codegen, minified CSS, binary).
COPY . .
RUN touch .env.local && mise run build

# Prod: slim Debian, compiled binary + static assets only.
FROM debian:trixie-slim AS prod

RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl \
  && rm -rf /var/lib/apt/lists/* \
  && useradd -m -s /usr/sbin/nologin app

WORKDIR /app
COPY --from=builder /src/tmp/app ./app
COPY --from=builder /src/static ./static
RUN chown -R app:app /app
USER app

EXPOSE 8080
CMD ["./app"]
