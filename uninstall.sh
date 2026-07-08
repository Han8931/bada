#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PREFIX="/usr/local"
BIN_NAME="bada"
PURGE=0

usage() {
  cat <<'EOF'
Usage: ./uninstall.sh [--prefix DIR] [--bin-name NAME] [--purge]

Removes the bada binary installed by install.sh. By default it removes:
  - the installed binary in DIR/bin (default: /usr/local/bin)
  - the local build artifact under ./bin
  - any other copies of the binary found on your PATH (stale shadowing copies)

With --purge it also deletes user data:
  - config dir:  ${XDG_CONFIG_HOME:-~/.config}/bada
  - data dir:    ${XDG_DATA_HOME:-~/.local/share}/bada (the DB and trash)
  - cache dir:   ${XDG_CACHE_HOME:-~/.cache}/bada (legacy DB location)
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix)
      PREFIX="${2:-}"
      shift 2
      ;;
    --bin-name)
      BIN_NAME="${2:-}"
      shift 2
      ;;
    --purge)
      PURGE=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

# Remove a file with sudo fallback when we lack write access to its directory.
remove_file() {
  local path="$1"
  [[ -e "${path}" ]] || return 0
  local dir
  dir="$(dirname "${path}")"
  if [[ -w "${dir}" ]]; then
    rm -f "${path}"
  else
    echo "No write access to ${dir}. Trying sudo..."
    sudo rm -f "${path}"
  fi
  echo "Removed ${path}"
}

BIN_DIR="${PREFIX}/bin"
TARGET="${BIN_DIR}/${BIN_NAME}"

# 1. Remove the primary installed binary.
remove_file "${TARGET}"

# 2. Remove the local build artifact.
remove_file "${ROOT_DIR}/bin/${BIN_NAME}"

# 3. Hunt down every other copy on PATH so no stale version keeps running.
#    `command -v` only finds the first; loop until PATH is clean.
while RESOLVED="$(command -v "${BIN_NAME}" 2>/dev/null || true)"; [[ -n "${RESOLVED}" ]]; do
  echo "Found another '${BIN_NAME}' on PATH: ${RESOLVED}"
  remove_file "${RESOLVED}"
  # Guard against a non-removable hit (e.g. a shell builtin/alias) to avoid an
  # infinite loop.
  if [[ "$(command -v "${BIN_NAME}" 2>/dev/null || true)" == "${RESOLVED}" ]]; then
    echo "Could not remove ${RESOLVED}; stop here." >&2
    break
  fi
done

# 4. Optionally purge user data.
if [[ "${PURGE}" -eq 1 ]]; then
  CONFIG_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/bada"
  DATA_DIR="${XDG_DATA_HOME:-${HOME}/.local/share}/bada"
  CACHE_DIR="${XDG_CACHE_HOME:-${HOME}/.cache}/bada"
  for dir in "${CONFIG_DIR}" "${DATA_DIR}" "${CACHE_DIR}"; do
    if [[ -d "${dir}" ]]; then
      rm -rf "${dir}"
      echo "Purged ${dir}"
    fi
  done
fi

echo "Uninstall complete."
