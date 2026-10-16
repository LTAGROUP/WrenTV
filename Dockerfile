# syntax=docker/dockerfile:1

FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/wrentv ./cmd/wrentv

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

COPY --from=build /out/wrentv /usr/local/bin/wrentv

ENV WRENTV_DATA_DIR=/config \
    WRENTV_FFMPEG=/usr/lib/jellyfin-ffmpeg/ffmpeg \
    WRENTV_FFPROBE=/usr/lib/jellyfin-ffmpeg/ffprobe \
    NVIDIA_DRIVER_CAPABILITIES=compute,video,utility

VOLUME ["/config"]
# 32500: API, web app and streaming.
EXPOSE 32500
HEALTHCHECK --interval=30s --timeout=5s CMD curl -fsS http://127.0.0.1:32500/api/health || exit 1
ENTRYPOINT ["/usr/local/bin/wrentv"]
