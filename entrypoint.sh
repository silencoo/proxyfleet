#!/bin/sh
set -eu
umask 077

for path in /etc/proxyfleet/config.yaml /etc/proxyfleet/nodes.txt; do
  if [ -L "$path" ] || { [ -e "$path" ] && [ ! -f "$path" ]; }; then
    echo "Expected a regular file: $path. Repair the mount; no fallback configuration was created." >&2
    exit 1
  fi
done

if [ "$(id -u)" = 0 ]; then
  runtime_uid="${PROXYFLEET_UID:-10001}"
  runtime_gid="${PROXYFLEET_GID:-10001}"
  case "$runtime_uid" in ''|*[!0-9]*) echo "PROXYFLEET_UID must be numeric" >&2; exit 1;; esac
  case "$runtime_gid" in ''|*[!0-9]*) echo "PROXYFLEET_GID must be numeric" >&2; exit 1;; esac
  if [ "$runtime_uid" -eq 0 ]; then
    echo "PROXYFLEET_UID must be non-root" >&2
    exit 1
  fi
  mkdir -p /etc/proxyfleet /app/logs
  # Do not follow symlinks or change ownership of /app and the executable.
  find /etc/proxyfleet /app/logs -type d -exec chown "$runtime_uid:$runtime_gid" {} + -exec chmod 700 {} +
  find /etc/proxyfleet /app/logs -type f -exec chown "$runtime_uid:$runtime_gid" {} + -exec chmod 600 {} +
  exec gosu "$runtime_uid:$runtime_gid" /usr/local/bin/proxyfleet "$@"
fi

# Explicit docker --user is supported when the mounted directories are already
# owned by that user. Fail on unusable mounts instead of hiding permission errors.
test -w /etc/proxyfleet
test -w /app/logs
exec /usr/local/bin/proxyfleet "$@"
