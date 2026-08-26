# syntax=docker/dockerfile:1

# ---- Stage 1: builder ------------------------------------------------------
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Copy module files first and download dependencies separately, so this
# layer is cached and skipped on rebuilds that only change source code.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 produces fully static binaries, required for a distroless
# base with no libc to link against.
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/healthcheck ./cmd/healthcheck

# ---- Stage 2: final image ---------------------------------------------------
# distroless/static: no shell, no package manager, no libc — just enough
# to run a static Go binary. The :nonroot tag already runs as UID/GID
# 65532 (user "nonroot"); USER is set explicitly below anyway for clarity.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/server /server
COPY --from=builder /out/healthcheck /healthcheck

USER nonroot:nonroot

# Default; actual port is configurable via the PORT env var (see
# cmd/server/config.go) — update this if you also change PORT.
EXPOSE 8080

# distroless has no shell, so HEALTHCHECK must invoke an executable
# directly (exec form) rather than a shell command line — that's exactly
# what cmd/healthcheck exists for.
HEALTHCHECK --interval=30s --timeout=3s --retries=3 CMD ["/healthcheck"]

ENTRYPOINT ["/server"]
