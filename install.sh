#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PREFIX="/usr/local"
BIN_NAME="bada"

usage() {
  cat <<'EOF'
Usage: ./install.sh [--prefix DIR] [--bin-name NAME]

Builds the bada binary and installs it into DIR/bin (default: /usr/local/bin).
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

if [[ -z "${PREFIX}" ]]; then
  echo "Prefix is required." >&2
  exit 1
fi

CONFIG_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/bada"
CACHE_DIR="${XDG_CACHE_HOME:-${HOME}/.cache}/bada"
CONFIG_PATH="${CONFIG_DIR}/config.toml"
DB_PATH="${CACHE_DIR}/bada.db"
TRASH_DIR="${CACHE_DIR}/trash"

mkdir -p "${CONFIG_DIR}" "${CACHE_DIR}"

if [[ ! -f "${CONFIG_PATH}" ]]; then
  tmpfile="$(mktemp)"
  sed -e "s|^db_path = .*|db_path = \"${DB_PATH}\"|" \
      -e "s|^trash_dir = .*|trash_dir = \"${TRASH_DIR}\"|" \
      "${ROOT_DIR}/config.example.toml" > "${tmpfile}"
  mv "${tmpfile}" "${CONFIG_PATH}"
  echo "Wrote default config to ${CONFIG_PATH}"
fi

BIN_DIR="${PREFIX}/bin"
TARGET="${BIN_DIR}/${BIN_NAME}"
LOCAL_BIN="${ROOT_DIR}/bin/${BIN_NAME}"

# Clean build: drop any stale local artifact so we never re-install an old
# binary, and rebuild from the current sources.
echo "Cleaning previous build artifacts..."
rm -f "${LOCAL_BIN}"
go clean -cache >/dev/null 2>&1 || true

echo "Building ${BIN_NAME}..."
mkdir -p "${ROOT_DIR}/bin"
GOFLAGS=${GOFLAGS:-}
go build ${GOFLAGS} -o "${LOCAL_BIN}" "${ROOT_DIR}/cmd/todo"

echo "Installing to ${TARGET}..."
if [[ -w "${BIN_DIR}" ]]; then
  install -m 0755 "${LOCAL_BIN}" "${TARGET}"
else
  echo "No write access to ${BIN_DIR}. Trying sudo..."
  sudo install -m 0755 "${LOCAL_BIN}" "${TARGET}"
fi

echo "Installed ${BIN_NAME} to ${TARGET}"

# Guard against the classic "old version keeps running" trap: another copy of
# the binary sitting earlier on PATH shadows the one we just installed.
RESOLVED="$(command -v "${BIN_NAME}" 2>/dev/null || true)"
if [[ -n "${RESOLVED}" && "${RESOLVED}" != "${TARGET}" ]]; then
  echo
  echo "WARNING: '${BIN_NAME}' on your PATH resolves to:" >&2
  echo "    ${RESOLVED}" >&2
  echo "but it was just installed to:" >&2
  echo "    ${TARGET}" >&2
  echo "The shadowing copy will run instead of the new build. Remove it with:" >&2
  echo "    rm -f '${RESOLVED}'" >&2
  echo "(or run ./uninstall.sh, which clears known stale copies), then re-open your shell." >&2
elif [[ -z "${RESOLVED}" ]]; then
  echo
  echo "NOTE: ${BIN_DIR} is not on your PATH. Add it, e.g.:" >&2
  echo "    export PATH=\"${BIN_DIR}:\$PATH\"" >&2
fi
