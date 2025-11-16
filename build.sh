#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
PACKAGE="github.com/gamzabox/humble-ai-cli/internal/buildinfo"

if command -v git >/dev/null 2>&1; then
	VERSION="$(git -C "${ROOT_DIR}" describe --tags --abbrev=0 2>/dev/null || true)"
else
	VERSION=""
fi

if [[ -z "${VERSION}" ]]; then
	VERSION="dev"
fi

BUILD_DATE="$(date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || true)"
if [[ -z "${BUILD_DATE}" ]]; then
	BUILD_DATE="unknown"
fi

LDFLAGS="-X ${PACKAGE}.Version=${VERSION} -X ${PACKAGE}.Date=${BUILD_DATE}"

mkdir -p "${DIST_DIR}"

build_target() {
	local goos="$1"
	local goarch="$2"
	local outfile="$3"

	echo "Building ${goos}/${goarch} -> ${outfile} (version=${VERSION}, date=${BUILD_DATE})"
	GOOS="${goos}" GOARCH="${goarch}" CGO_ENABLED=0 \
		go build -trimpath -ldflags "${LDFLAGS}" -o "${DIST_DIR}/${outfile}" .
}

build_target linux amd64 humble-ai-cli
build_target windows amd64 humble-ai-cli_amd64.exe
build_target windows arm64 humble-ai-cli_arm64.exe

echo "Artifacts written to ${DIST_DIR}"
