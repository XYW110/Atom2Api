FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.22-alpine AS backend
WORKDIR /src
ARG VERSION=dev
COPY go.mod go.sum ./
COPY *.go ./
COPY --from=frontend /src/frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/atom2api .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S atom2api \
    && adduser -S -G atom2api atom2api \
    && mkdir -p /data \
    && chown atom2api:atom2api /data
COPY --from=backend /out/atom2api /usr/local/bin/atom2api
WORKDIR /data
USER atom2api
ENV ATOM2API_CONFIG=/data/config.json \
    TZ=Asia/Shanghai
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/atom2api"]
