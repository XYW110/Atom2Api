# syntax=docker/dockerfile:1
# Compile Node/Go on the builder's native arch. Only the final image is
# TARGETPLATFORM, so GitHub-hosted runners no longer QEMU-emulate npm/go.
FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend
WORKDIR /src/frontend
ENV NODE_OPTIONS=--max-old-space-size=2048
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS backend
WORKDIR /src
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY --from=frontend /src/frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/atom2api .

FROM --platform=$BUILDPLATFORM alpine:3.20 AS runtime-prep
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S atom2api \
    && adduser -S -G atom2api atom2api \
    && mkdir -p /data \
    && chown atom2api:atom2api /data

# No RUN in the target image, so arm64 does not need QEMU on GitHub-hosted runners.
FROM alpine:3.20
COPY --from=runtime-prep /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=runtime-prep /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=runtime-prep /etc/passwd /etc/passwd
COPY --from=runtime-prep /etc/group /etc/group
COPY --from=runtime-prep --chown=atom2api:atom2api /data /data
COPY --from=backend --chown=root:root /out/atom2api /usr/local/bin/atom2api
WORKDIR /data
USER atom2api
ENV ATOM2API_CONFIG=/data/config.json \
    TZ=Asia/Shanghai
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/atom2api"]
