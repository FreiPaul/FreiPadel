# ---- Frontend build ----
# --platform=$BUILDPLATFORM pins this stage to the *builder's* native arch so
# npm runs without emulation; its output (JS) is architecture-independent.
FROM --platform=$BUILDPLATFORM node:26-alpine AS frontend
WORKDIR /app
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# ---- Backend build ----
# Build natively on the builder arch (no QEMU) and cross-compile to the target
# arch via GOARCH. Pure-Go (CGO_ENABLED=0) makes this free and exact.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS backend
WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o /freipadel .

# ---- Runtime ----
# Pinned minor version, so a rebuild never jumps to a new Alpine release
# unnoticed (Dependabot bumps it).
FROM alpine:3.24
RUN apk add --no-cache ca-certificates \
 && mkdir /data && chown 10001:10001 /data
WORKDIR /app
COPY --from=backend /freipadel ./freipadel
COPY --from=frontend /app/build ./static

# Run without root. The binary and static files stay owned by root, so the
# process cannot modify them; only /data must be writable for this user.
# Production overrides the uid with the owner of the server's ./data
# directory (see docker-compose-prod.yml and the Makefile).
USER 10001:10001

ENV DATA_DIR=/data \
    STATIC_DIR=/app/static \
    PORT=8080
VOLUME /data
EXPOSE 8080

CMD ["./freipadel"]
