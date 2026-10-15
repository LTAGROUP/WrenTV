# syntax=docker/dockerfile:1

FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/haven ./cmd/haven

# jellyfin-ffmpeg ships zscale/tonemapping plus NVENC, QSV, VAAPI and AMF
# support with the needed drivers bundled.
FROM debian:bookworm-slim
ARG TARGETARCH
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl gnupg \
 && install -d /etc/apt/keyrings \
 && curl -fsSL https://repo.jellyfin.org/jellyfin_team.gpg.key | gpg --dearmor -o /etc/apt/keyrings/jellyfin.gpg \
 && echo "deb [arch=${TARGETARCH} signed-by=/etc/apt/keyrings/jellyfin.gpg] https://repo.jellyfin.org/debian bookworm main" > /etc/apt/sources.list.d/jellyfin.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends jellyfin-ffmpeg7 \
 && apt-get purge -y gnupg && apt-get autoremove -y \
 && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/haven /usr/local/bin/haven

ENV HAVEN_DATA_DIR=/config \
    HAVEN_FFMPEG=/usr/lib/jellyfin-ffmpeg/ffmpeg \
    HAVEN_FFPROBE=/usr/lib/jellyfin-ffmpeg/ffprobe \
    NVIDIA_DRIVER_CAPABILITIES=compute,video,utility

VOLUME ["/config"]
# 32500: API/streaming. 42069: torrent peer port (TCP+UDP, inbound only; Haven never seeds).
EXPOSE 32500 42069 42069/udp
HEALTHCHECK --interval=30s --timeout=5s CMD curl -fsS http://127.0.0.1:32500/api/health || exit 1
ENTRYPOINT ["/usr/local/bin/haven"]
