# Multi-stage build for Bloom. Contract: coding-handbook golang/operations/deployment.md,
# extended with a Node stage per decisions/0002-embed-spa-in-binary.md.

# ---- web stage ------------------------------------------------------------
# Builds the SPA into internal/api/web/dist, which the Go binary embeds.
FROM node:24.21.0-bookworm-slim AS web

WORKDIR /src/web

COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY web/ ./
# vite.config.ts writes to ../internal/api/web/dist, i.e. /src/internal/api/web/dist.
RUN npm run build

# ---- build stage ----------------------------------------------------------
FROM golang:1.27.1 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web /src/internal/api/web/dist ./internal/api/web/dist

ARG VERSION=dev
ARG COMMIT=unknown

# Static binary (CGO_ENABLED=0): modernc.org/sqlite and pgx are pure Go.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w \
        -X github.com/BonzTM/bloom/internal/buildinfo.Name=bloom \
        -X github.com/BonzTM/bloom/internal/buildinfo.Version=${VERSION} \
        -X github.com/BonzTM/bloom/internal/buildinfo.Commit=${COMMIT}" \
      -o /out/bloom ./cmd/bloom

# ---- runtime stage --------------------------------------------------------
FROM gcr.io/distroless/static-debian13:nonroot

ARG VERSION=dev
ARG COMMIT=unknown
ARG CREATED=
LABEL org.opencontainers.image.source="https://github.com/BonzTM/bloom" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${CREATED}"

COPY --from=build /out/bloom /bloom

# SQLite default lives here; mount a volume. PostgreSQL users set BLOOM_DB_DRIVER=postgres.
VOLUME ["/data"]
ENV BLOOM_DB_DSN="file:/data/bloom.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/bloom"]
