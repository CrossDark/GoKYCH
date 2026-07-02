#!/usr/bin/env bash
# Shared helpers for the scripts/ directory. Source this from the other
# scripts after `set -euo pipefail` to pull in the colour logging,
# platform detection, and SHA256 verification routines that were
# previously copy-pasted across build-release.sh / install-backend.sh /
# install-all.sh / deploy-backend.sh.
#
# Usage:
#   source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

RED='\033[0;31m'; GRN='\033[0;32m'; YEL='\033[0;33m'; BLU='\033[0;34m'; NC='\033[0m'
log()  { printf "${BLU}==>${NC} %s\n" "$*"; }
ok()   { printf "${GRN}✓${NC} %s\n" "$*"; }
warn() { printf "${YEL}!${NC} %s\n" "$*" >&2; }
die()  { printf "${RED}✗${NC} %s\n" "$*" >&2; exit 1; }

# detect_platform prints "GOOS/GOARCH" for the current host, normalised to
# the asset naming used by build-release.sh (e.g. "darwin/arm64").
detect_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m | tr '[:upper:]' '[:lower:]')"
  case "$os" in
    darwin) os="darwin" ;;
    linux)  os="linux"  ;;
    *) die "unsupported OS: $os" ;;
  esac
  case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *) die "unsupported arch: $arch" ;;
  esac
  printf '%s/%s' "$os" "$arch"
}

# verify_sha256 checks a downloaded file against a SHA256SUMS manifest.
# Args: <file_path> <expected_sha256> . Exits non-zero on mismatch.
verify_sha256() {
  local file="$1" expected="$2" actual
  if [[ ! -f "$file" ]]; then
    die "file not found: $file"
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$file" | awk '{print $1}')"
  else
    # macOS shasum fallback.
    actual="$(shasum -a 256 "$file" | awk '{print $1}')"
  fi
  if [[ "$actual" != "$expected" ]]; then
    die "sha256 mismatch for $file
  expected: $expected
  actual:   $actual"
  fi
  ok "sha256 verified: $file"
}
