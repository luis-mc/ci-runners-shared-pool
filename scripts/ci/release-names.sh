#!/usr/bin/env bash
# OPS69: release asset names. Every release asset is named Crsp-<version>-<os>-<arch>.<ext>:
#   version  SemVer without a leading v (an edge build carries edge-<sha>)
#   os       win | linux | macos
#   arch     x64 | arm64
#   ext      msi | deb | rpm | pkg | tar.gz | zip
# Example: Crsp-0.1.22-win-x64.msi. The build scripts and CI artifacts keep their stable names
# (crsp-<goos>-<goarch>.<ext>, DL-22); the publish job renames the files with this function.
# Usage: source scripts/ci/release-names.sh; asset_name 0.1.22 windows amd64 msi
#        bash scripts/ci/release-names.sh 0.1.22 windows amd64 msi

asset_os() {
  case "$1" in
    windows|win) echo win ;;
    linux) echo linux ;;
    darwin|macos) echo macos ;;
    *) echo "release-names: unknown os $1" >&2; return 1 ;;
  esac
}

asset_arch() {
  case "$1" in
    amd64|x64|x86_64) echo x64 ;;
    arm64|aarch64) echo arm64 ;;
    *) echo "release-names: unknown arch $1" >&2; return 1 ;;
  esac
}

# asset_name VERSION OS ARCH EXT; OS and ARCH may be Go names (darwin, amd64) or release names.
asset_name() {
  local version="${1#v}" os arch
  os="$(asset_os "$2")" || return 1
  arch="$(asset_arch "$3")" || return 1
  printf 'Crsp-%s-%s-%s.%s\n' "$version" "$os" "$arch" "$4"
}

# stable_to_asset VERSION FILE: the stable name crsp-<goos>-<goarch>.<ext> of a build script's file to its release name.
stable_to_asset() {
  local version="$1" base os arch ext rest
  base="$(basename "$2")"
  ext="${base#*.}"          # msi, deb, rpm, pkg, tar.gz, zip
  rest="${base%%.*}"        # crsp-<goos>-<goarch>
  rest="${rest#crsp-}"
  os="${rest%%-*}"
  arch="${rest#*-}"
  asset_name "$version" "$os" "$arch" "$ext"
}

# CRSP224, TRANSITION: the name the updater of 0.1.21 and older looks for,
# crsp-<goos>-<goarch>_<version>.<ext> (internal/update/source.go of v0.1.21, VersionedName). os:
# win->windows, linux->linux, macos->darwin; arch: x64->amd64, arm64->arm64. Kept until no
# supported install is on 0.1.21 or older (operations R5.127).
# legacy_asset_name VERSION OS ARCH EXT; OS and ARCH may be Go names or release names.
legacy_asset_name() {
  local version="${1#v}" os arch
  case "$2" in
    windows|win) os=windows ;;
    linux) os=linux ;;
    darwin|macos) os=darwin ;;
    *) echo "release-names: unknown os $2" >&2; return 1 ;;
  esac
  case "$3" in
    amd64|x64|x86_64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) echo "release-names: unknown arch $3" >&2; return 1 ;;
  esac
  printf 'crsp-%s-%s_%s.%s\n' "$os" "$arch" "$version" "$4"
}

# release_to_legacy VERSION FILE: the legacy name of a Crsp-<version>-<os>-<arch>.<ext> asset.
release_to_legacy() {
  local version="${1#v}" base rest os arch ext
  base="$(basename "$2")"
  rest="${base#Crsp-"$version"-}"
  [ "$rest" != "$base" ] || { echo "release-names: $base does not carry version $version" >&2; return 1; }
  ext="${rest#*.}"
  rest="${rest%%.*}"
  os="${rest%%-*}"
  arch="${rest#*-}"
  legacy_asset_name "$version" "$os" "$arch" "$ext"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  if [ "${1:-}" = "--legacy" ]; then shift; legacy_asset_name "$@"; else asset_name "$@"; fi
fi
