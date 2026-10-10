#!/usr/bin/env bash
# Produce desktop binaries, a docker-load archive and corresponding sources.
set -euo pipefail
cd "$(dirname "$0")"
version=${1:-dev}
if [[ $# -gt 1 || ! $version =~ ^(dev|v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?)$ || ${#version} -gt 128 ]]; then
    echo 'Usage: ./build-container.sh [dev|vMAJOR.MINOR.PATCH[-prerelease]]' >&2
    exit 2
fi
for command in docker python3 gzip git; do command -v "$command" >/dev/null; done
docker info >/dev/null
docker buildx version >/dev/null
output="$PWD/target/releases/$version"
if [[ -e "$output" ]]; then
    echo "Output already exists: $output. Move or remove it before rebuilding." >&2
    exit 1
fi
mkdir -p "$output"
sources=$(mktemp -d "$PWD/target/.release-sources-XXXXXX")
trap 'rm -rf "$sources"' EXIT
for system in linux windows; do
    docker buildx build --platform linux/amd64 --target artifacts \
        --build-arg TARGET_OS="$system" --build-arg BUILD_MODE=desktop \
        --output "type=local,dest=$output" .
done
docker buildx build --platform linux/amd64 --target runtime \
    --build-arg TARGET_OS=linux --build-arg BUILD_MODE=headless \
    --build-arg VERSION="$version" --build-arg REVISION="$(git rev-parse HEAD)" \
    --tag "shelfmark:$version" --load .
docker image save "shelfmark:$version" | gzip > "$output/shelfmark-docker-linux-amd64.tar.gz"
docker buildx build --platform linux/amd64 --target source-export \
    --build-arg TARGET_OS=linux --build-arg BUILD_MODE=headless \
    --output "type=local,dest=$sources" .
# Include tracked and nonignored source files, including local edits. Keep
# ignored data, dependency caches and generated payloads out of the download.
git ls-files --cached --others --exclude-standard -z | \
    tar --null -T - -czf "$output/shelfmark-source.tar.gz"
python3 tools/package_release.py --sources "$sources" --output "$output"
echo "Release files: $output"
