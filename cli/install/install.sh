#!/usr/bin/env sh
set -eu

DEFAULT_REPO="astraive/loza"
REPO="${LOZA_REPO:-$DEFAULT_REPO}"
INSTALL_DIR="${LOZA_INSTALL_DIR:-$HOME/.local/bin}"
TMP_DIR="$(mktemp -d)"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "loza installer requires $1" >&2
    exit 1
  }
}

fail() {
  echo "loza installer: $*" >&2
  exit 1
}

# Keep the override (e.g. for case variants) but pin it to the official repo.
# A poisoned environment must not be able to point the installer at an
# attacker-controlled release, including its checksum sidecar.
repo_lc="$(printf '%s' "$REPO" | tr '[:upper:]' '[:lower:]')"
case "$repo_lc" in
  "$DEFAULT_REPO") ;;
  *) fail "LOZA_REPO must be $DEFAULT_REPO; arbitrary repositories are not allowed" ;;
esac

need curl
need tar
need tr
need awk

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$1" | awk '{print $NF}'
  else
    fail "SHA-256 tool (sha256sum, shasum, or openssl) required to verify releases"
  fi
}

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"

case "$os" in
  linux) os="linux" ;;
  darwin) os="darwin" ;;
  *) echo "unsupported OS: $os" >&2; exit 1 ;;
esac

case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac

api="https://api.github.com/repos/$REPO/releases"
releases="$TMP_DIR/releases.json"
curl -fsSL --proto '=https' --proto-redir '=https' "$api" -o "$releases"

# Split on commas so this works with GitHub's compact JSON as well as pretty
# output, and pick the newest release that ships an archive for this platform.
asset_url="$(
  tr ',' '\n' < "$releases" |
    sed -n 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"\(.*loza_.*_'"$os"'_'"$arch"'\.tar\.gz\)".*/\1/p' |
    head -n 1
)"

if [ -z "$asset_url" ]; then
  echo "could not find a LOZA CLI release asset for $os/$arch" >&2
  exit 1
fi

case "$asset_url" in
  https://github.com/*) ;;
  *) fail "refusing non-HTTPS or untrusted asset URL: $asset_url" ;;
esac

mkdir -p "$INSTALL_DIR"
archive="$TMP_DIR/loza.tar.gz"
curl -fsSL --proto '=https' --proto-redir '=https' "$asset_url" -o "$archive"

# The release workflow publishes "<asset>.sha256" beside every archive. A
# missing, malformed, or mismatched checksum aborts before anything is installed.
checksum_url="$asset_url.sha256"
case "$checksum_url" in
  https://github.com/*) ;;
  *) fail "refusing non-HTTPS or untrusted checksum URL: $checksum_url" ;;
esac
checksum_file="$archive.sha256"
curl -fsSL --proto '=https' --proto-redir '=https' "$checksum_url" -o "$checksum_file" ||
  fail "could not download release checksum ($checksum_url)"

expected=""
checksum_name=""
IFS=' ' read -r expected checksum_name _ < "$checksum_file" || true
case "$expected" in
  *[!0-9a-fA-F]*|'') fail "malformed release checksum" ;;
esac
[ "${#expected}" -eq 64 ] || fail "malformed release checksum"

asset_name="${asset_url##*/}"
[ "$checksum_name" = "$asset_name" ] || fail "release checksum is for a different asset"

actual="$(sha256_file "$archive")"
[ "$actual" = "$expected" ] || fail "checksum mismatch: release asset did not match published SHA-256"

tar -xzf "$archive" -C "$TMP_DIR"
install -m 0755 "$TMP_DIR/loza" "$INSTALL_DIR/loza"

echo "Installed loza to $INSTALL_DIR/loza (SHA-256 verified)"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "Add $INSTALL_DIR to PATH to run loza from any shell." ;;
esac
