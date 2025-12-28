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

BIN_DIR="${PREFIX}/bin"
TARGET="${BIN_DIR}/${BIN_NAME}"

echo "Building ${BIN_NAME}..."
mkdir -p "${ROOT_DIR}/bin"
GOFLAGS=${GOFLAGS:-}
go build ${GOFLAGS} -o "${ROOT_DIR}/bin/${BIN_NAME}" "${ROOT_DIR}/cmd/todo"

echo "Installing to ${TARGET}..."
if [[ -w "${BIN_DIR}" ]]; then
  install -m 0755 "${ROOT_DIR}/bin/${BIN_NAME}" "${TARGET}"
else
  echo "No write access to ${BIN_DIR}. Trying sudo..."
  sudo install -m 0755 "${ROOT_DIR}/bin/${BIN_NAME}" "${TARGET}"
fi

echo "Installed ${BIN_NAME} to ${TARGET}"
