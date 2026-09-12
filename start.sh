#!/bin/sh
# Build first so a failed update leaves the running instance intact.
set -eu
umask 077
cd -- "$(dirname -- "$0")"

export PROXYFLEET_DATA_DIR="${PROXYFLEET_DATA_DIR:-./data}"
export PROXYFLEET_UID="${PROXYFLEET_UID:-$(id -u)}"
export PROXYFLEET_GID="${PROXYFLEET_GID:-$(id -g)}"
if [ "$PROXYFLEET_UID" = 0 ]; then
  export PROXYFLEET_UID=10001
  export PROXYFLEET_GID=10001
fi

# Legacy single-file mounts may have additional state only inside the container.
# Never replace them with an empty directory or delete a mistaken directory.
if [ ! -f "$PROXYFLEET_DATA_DIR/config.yaml" ] && { [ -e config.yaml ] || [ -L config.yaml ] || [ -e nodes.txt ] || [ -L nodes.txt ]; }; then
  echo "Legacy config.yaml/nodes.txt detected. Migrate the stopped container and host files to $PROXYFLEET_DATA_DIR first; see docs/docker-deployment.md." >&2
  exit 1
fi
for directory in "$PROXYFLEET_DATA_DIR" ./logs; do
  if [ -L "$directory" ] || { [ -e "$directory" ] && [ ! -d "$directory" ]; }; then
    echo "Expected a real directory: $directory; no files were removed." >&2
    exit 1
  fi
done
for name in config.yaml nodes.txt; do
  path="$PROXYFLEET_DATA_DIR/$name"
  if [ -L "$path" ] || { [ -e "$path" ] && [ ! -f "$path" ]; }; then
    echo "Expected a regular file: $path; no files were removed." >&2
    exit 1
  fi
done

docker compose build

# The application creates its safe first-run configuration and random password.
# Existing configuration is never replaced with an example or an empty file.
mkdir -p "$PROXYFLEET_DATA_DIR" ./logs
chmod 700 "$PROXYFLEET_DATA_DIR" ./logs
exec docker compose up -d --no-build
