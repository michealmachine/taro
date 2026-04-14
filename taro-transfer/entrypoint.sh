#!/bin/sh
set -eu

RCLONE_CONFIG_DIR="${RCLONE_CONFIG_DIR:-/root/.config/rclone}"
RCLONE_CONFIG_PATH="${RCLONE_CONFIG_PATH:-$RCLONE_CONFIG_DIR/rclone.conf}"

mkdir -p "$RCLONE_CONFIG_DIR"

if [ -n "${RCLONE_CONFIG_B64:-}" ]; then
  printf '%s' "$RCLONE_CONFIG_B64" | base64 -d > "$RCLONE_CONFIG_PATH"
  chmod 600 "$RCLONE_CONFIG_PATH"
fi

if [ ! -s "$RCLONE_CONFIG_PATH" ]; then
  echo "ERROR: rclone config missing. Provide RCLONE_CONFIG_B64 or mount $RCLONE_CONFIG_PATH" >&2
  exit 1
fi

if [ -z "${TARO_TRANSFER_TOKEN:-}" ]; then
  echo "ERROR: TARO_TRANSFER_TOKEN is required" >&2
  exit 1
fi

exec /app/taro-transfer
