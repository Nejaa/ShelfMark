#!/bin/sh
set -eu
# Log and OCR model symlinks point into the same persisted state volume.
mkdir -p /data/tessdata
exec /opt/shelfmark/shelfmark -headless -listen 0.0.0.0:8766 -data-dir /data "$@"
