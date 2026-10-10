# syntax=docker/dockerfile:1.7
# Linux/amd64 is the build host; MinGW cross-compiles the Windows helper and OCR.
FROM golang:1.27.2-bookworm AS dependencies
ENV GOTOOLCHAIN=local
WORKDIR /work
COPY go.mod go.sum ./
RUN go mod download

FROM dependencies AS checks
COPY . .
RUN CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./... \
    && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./... \
    && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...

FROM dependencies AS native-tools
# Keep binary and source repositories aligned. Exact source collection fails
# rather than silently substituting a different version of a bundled library.
RUN sed -i 's/^Types: deb$/Types: deb deb-src/' /etc/apt/sources.list.d/debian.sources \
    && apt-get update && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
       build-essential cmake pkg-config python3 cabextract \
       gcc-mingw-w64-x86-64 g++-mingw-w64-x86-64 \
       libgtk-3-dev libwebkit2gtk-4.0-dev libglib2.0-bin glib-networking \
       libgdk-pixbuf2.0-bin libegl-mesa0 libglx-mesa0 libgl1-mesa-dri \
       bubblewrap xdg-dbus-proxy xkb-data fonts-dejavu-core adwaita-icon-theme \
    && dpkg-query -W '-f=${binary:Package}\t${Version}\t${source:Package}\t${source:Version}\t${Architecture}\n' > /build-packages.tsv

FROM native-tools AS builder
ARG TARGET_OS=linux
ARG TARGET_ARCH=amd64
# Compile OCR before copying application code so ordinary edits reuse this layer.
COPY runtime/ocr/ runtime/ocr/
RUN test "$TARGET_ARCH" = amd64 && case "$TARGET_OS" in linux|windows) ;; *) exit 1 ;; esac \
    && python3 runtime/ocr/package.py --target-os "$TARGET_OS" --target-arch "$TARGET_ARCH" \
       --cache /work/target/cache/ocr --output /prebuilt-ocr.tar.gz
COPY desktop/webview/ desktop/webview/
RUN if [ "$TARGET_OS" = windows ]; then \
       python3 -m desktop.webview.windows.runtime --arch "$TARGET_ARCH" --cache /work/target/cache/webview2; \
    fi
COPY . .
ARG BUILD_MODE=desktop
RUN case "$BUILD_MODE" in \
      desktop) bash build-target.sh "$TARGET_OS" "$TARGET_ARCH" ;; \
      headless) bash build-target.sh "$TARGET_OS" "$TARGET_ARCH" --headless ;; \
      *) echo 'BUILD_MODE must be desktop or headless' >&2; exit 1 ;; \
    esac \
    && mkdir /out \
    && cp target/shelfmark-"$TARGET_OS"-"$TARGET_ARCH"* /out/

FROM scratch AS artifacts
COPY --from=builder /out/ /

FROM debian:bookworm-slim AS runtime-system
RUN apt-get update && apt-get upgrade -y && apt-get install -y --no-install-recommends ca-certificates \
    && dpkg-query -W '-f=${binary:Package}\t${Version}\t${source:Package}\t${source:Version}\t${Architecture}\n' > /runtime-packages.tsv \
    && rm -rf /var/lib/apt/lists/*

FROM runtime-system AS runtime
ARG TARGET_OS=linux
ARG BUILD_MODE=desktop
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="Shelfmark" \
      org.opencontainers.image.description="Ebook metadata manager with a browser UI and bundled OCR" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.source="https://github.com/Nejaa/ShelfMark" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION"
# A container has no window; require a deliberately selected headless build.
RUN test "$TARGET_OS" = linux && test "$BUILD_MODE" = headless \
    && mkdir -p /opt/shelfmark /data/tessdata /books /usr/share/shelfmark/licenses \
    && ln -s /data/shelfmark.log /opt/shelfmark/shelfmark.log \
    && ln -s /data/tessdata /opt/shelfmark/tessdata \
    && chown -R 10001:10001 /data
COPY --from=builder /out/shelfmark-linux-amd64 /opt/shelfmark/shelfmark
COPY --from=builder /out/shelfmark-linux-amd64-licenses.tar.gz /usr/share/shelfmark/licenses/notices.tar.gz
COPY LICENSE THIRD_PARTY.md /usr/share/shelfmark/licenses/
COPY --chmod=755 docker/entrypoint.sh /usr/local/bin/shelfmark-entrypoint
ENV HOME=/data
USER 10001:10001
WORKDIR /books
EXPOSE 8766
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/shelfmark-entrypoint"]
STOPSIGNAL SIGTERM

# Source exports are separate from the runtime image. The superset includes
# build tools, native libraries and both compiler runtimes, retaining Debian's
# original archives, patch sets, source descriptors and package inventory.
FROM native-tools AS native-sources
COPY tools/debian_sources.py /collect-sources.py
RUN apt-get update && python3 /collect-sources.py --inventory /build-packages.tsv --output /sources/native

FROM runtime-system AS runtime-sources
COPY tools/debian_sources.py /collect-sources.py
RUN sed -i 's/^Types: deb$/Types: deb deb-src/' /etc/apt/sources.list.d/debian.sources \
    && apt-get update && apt-get install -y --no-install-recommends python3 \
    && python3 /collect-sources.py --inventory /runtime-packages.tsv --output /sources/runtime

FROM builder AS application-sources
RUN python3 tools/notices.py --ocr /prebuilt-ocr.tar.gz --output /source-notices.tar.gz --sources /sources/application

FROM scratch AS source-export
COPY --from=native-sources /sources/native /third-party-sources/native
COPY --from=runtime-sources /sources/runtime /third-party-sources/runtime
COPY --from=application-sources /sources/application /third-party-sources/application
COPY LICENSE THIRD_PARTY.md /third-party-sources/
