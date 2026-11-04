#!/bin/bash -p
# Download a published OpenAI CLI package before requesting installation privileges.

install_linux_release() (
  set -euo pipefail
  umask 077
  export LC_ALL=C

  fail() { printf 'OpenAI CLI: %s\n' "$1" >&2; exit 1; }
  github=https://github.com/openai/openai-cli
  [ "$(uname -s)" = Linux ] && [ "$EUID" -ne 0 ] || fail 'Run this installer as an ordinary Linux user.'
  command -v sudo >/dev/null || fail 'System packages require sudo. Use the Linux tarball without administrator access.'
  if command -v apt >/dev/null && command -v dpkg >/dev/null; then
    format=deb
    machine=$(dpkg --print-architecture)
  elif command -v dnf >/dev/null && command -v rpm >/dev/null; then
    format=rpm
    machine=$(rpm --eval '%{_arch}')
  else
    fail 'Use the Linux tarball on a system without apt or dnf.'
  fi
  case "$format:$machine" in
    deb:amd64|rpm:x86_64) arch=amd64 ;;
    deb:arm64|rpm:aarch64) arch=arm64 ;;
    deb:i386|rpm:i386|rpm:i486|rpm:i586|rpm:i686) arch=386 ;;
    deb:armhf|rpm:armv6hl|rpm:armv7hl) arch=armv6 ;;
    *) fail 'No release package supports this system architecture.' ;;
  esac

  transfer=(--fail --location --proto '=https' --proto-redir '=https' --connect-timeout 15 --retry 2 --show-error)
  # The protected publisher stamps its immutable tag here. A source checkout
  # discovers the stable tag through the same public Releases page as a browser.
  tag=''
  if [[ -z $tag ]]; then
    printf 'Finding the latest stable OpenAI CLI release...\n' >&2
    latest=$(curl -q "${transfer[@]}" --silent --head --output /dev/null --write-out '%{url_effective}' "$github/releases/latest")
    tag=${latest#"$github/releases/tag/"}
    [[ $latest == "$github/releases/tag/$tag" ]] || fail 'GitHub returned an unexpected release URL.'
  fi
  [[ $tag =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
    fail 'Expected an official stable OpenAI CLI release tag.'
  base="$github/releases/download/$tag"
  name="openai_${tag#v}_linux_$arch.$format"
  work=$(mktemp -d /tmp/openai-package.XXXXXXXX)
  trap 'rm -rf -- "$work"' EXIT
  trap 'exit 1' HUP INT TERM

  curl -q "${transfer[@]}" --silent --output "$work/checksums" "$base/openai_${tag#v}_checksums.txt" ||
    fail "Could not download the checksum manifest for $tag."
  digest=$(awk -v name="$name" '$2 == name {
    count++; value=$1; if (NF != 2 || length(value) != 64 || value !~ /^[0-9a-f]+$/) bad=1
  } END { if (count != 1 || bad) exit 1; print value }' "$work/checksums") ||
    fail "The release checksum manifest has no unique valid entry for $name."
  printf 'Downloading %s...\n' "$name" >&2
  curl -q "${transfer[@]}" --progress-bar --output "$work/$name" "$base/$name" ||
    fail "Could not download $name."
  printf '%s  %s\n' "$digest" "$work/$name" | sha256sum --check --status ||
    fail 'The downloaded package failed its SHA-256 check.'

  printf 'Verified %s. Installing the package...\n' "$name" >&2
  # The administrator receives only the opened package stream. Recheck the
  # root-owned copy before the package manager can execute any package content.
  sudo /bin/sh -eu -c '
    PATH=/usr/sbin:/usr/bin:/sbin:/bin; export PATH
    umask 077
    stage=$(mktemp -d /tmp/openai-system-package.XXXXXXXX)
    trap '\''rm -rf -- "$stage"'\'' EXIT
    trap '\''exit 1'\'' HUP INT TERM
    package="$stage/$1"
    cat > "$package"
    if ! printf "%s  %s\n" "$2" "$package" | sha256sum --check --status; then
      printf "%s\n" "OpenAI CLI: the staged package failed its SHA-256 check." >&2
      exit 1
    fi
    chmod 0755 "$stage"
    chmod 0644 "$package"
    case "$3" in
      deb) apt install --yes --no-remove "$package" ;;
      rpm) dnf install --assumeyes "$package" ;;
    esac
  ' sh "$name" "$digest" "$format" < "$work/$name"
)

if [[ -z ${BASH_SOURCE[0]:-} || ${BASH_SOURCE[0]} == "$0" ]]; then
  PATH=/usr/sbin:/usr/bin:/sbin:/bin
  export PATH
  install_linux_release
fi
