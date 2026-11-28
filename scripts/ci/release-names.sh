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
#        agent_asset_name 0.1.22 linux arm64   (CrspAgent-0.1.22-linux-arm64.tar.gz, CRSP304)

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

# agent_asset_name VERSION OS ARCH [EXT]: CRSP304, CRSP348. The agent installer, CrspAgent-<version>-<os>-<arch>.<ext>.
# Without EXT: the archive (zip for Windows, tar.gz for the rest), whose name stays stable for the console import
# (CRSP347). With EXT (msi, pkg, deb): the native package. They are release assets only: the Controller installer
# is the one people download (website R5.7); the controller fetches the agent installers itself.
agent_asset_name() {
  local version="${1#v}" os arch ext="${4:-}"
  os="$(asset_os "$2")" || return 1
  arch="$(asset_arch "$3")" || return 1
  if [ -z "$ext" ]; then ext=tar.gz; [ "$os" = win ] && ext=zip; fi
  printf 'CrspAgent-%s-%s-%s.%s\n' "$version" "$os" "$arch" "$ext"
}

# agent_stable_to_asset VERSION FILE: crspagent-<goos>-<goarch>.<ext> (installer/agent/build.sh, the native
# package scripts) to its release name; the extension is kept.
agent_stable_to_asset() {
  local base rest ext
  base="$(basename "$2")"
  ext="${base#*.}"
  rest="${base%%.*}"        # crspagent-<goos>-<goarch>
  rest="${rest#crspagent-}"
  agent_asset_name "$1" "${rest%%-*}" "${rest#*-}" "$ext"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  asset_name "$@"
fi
