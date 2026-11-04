#!/bin/bash -p
# Download a published OpenAI CLI package before requesting installation privileges.

linux_release_uptime() {
  local uptime _
  read -r uptime _ < /proc/uptime || return 1
  printf '%s\n' "${uptime%%.*}"
}

install_linux_release() (
  set -euo pipefail
  caller_umask=$(umask)
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

  transfer=(--fail --location --proto '=https' --proto-redir '=https' --connect-timeout 15)
  deadline=$(linux_release_uptime) || fail 'Could not read the system clock.'
  deadline=$((deadline + 1800))
  remaining() {
    now=$(linux_release_uptime) || fail 'Could not read the system clock.'
    left=$((deadline - now))
    (( left > 0 )) || fail 'The 30-minute wait for release availability expired. Retry later.'
  }
  # The protected publisher stamps its immutable tag here. A source checkout
  # discovers the stable tag through the same public Releases page as a browser.
  tag=''
  pinned=$tag
  stable='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
  select_latest() {
    local result next
    remaining
    result=$(curl -q "${transfer[@]}" --max-time "$left" --silent --head --output /dev/null \
      --write-out '%{http_code} %{url_effective}' "$github/releases/latest") || fail 'Could not find the public stable release.'
    next=${result#"200 $github/releases/tag/"}
    [[ $result == "200 $github/releases/tag/$next" && $next =~ $stable ]] || fail 'Expected an official stable OpenAI CLI release tag.'
    if [[ -n $tag && $tag != "$next" ]]; then
      printf '%s\n' "$tag" "$next" | sort -V -C || fail 'The public stable version moved backward. Stopped before installation.'
      printf 'A newer release is available. Checking %s...\n' "$next" >&2
    fi
    tag=$next
  }
  if [[ -z $pinned ]]; then
    printf 'Finding the latest stable OpenAI CLI release...\n' >&2
    select_latest
  else
    [[ $tag =~ $stable ]] || fail 'Expected an official stable OpenAI CLI release tag.'
  fi
  work=$(mktemp -d /tmp/openai-package.XXXXXXXX)
  trap 'rm -rf -- "$work"' EXIT
  trap 'exit 1' HUP INT TERM

  # Return to the outer loop only when Latest advances. Every other error stops the installer.
  download() {
    local asset=$1 output=$2 display=$3 selected=$tag code rc started probe=false notice_at=1800
    local -a options
    while :; do
      remaining
      if [[ $probe == true ]]; then
        options=(--max-time "$left" --silent --head --output /dev/null)
      elif [[ $display == --progress-bar ]]; then
        # A healthy package can take longer than the availability wait on a slow connection.
        started=$now
        options=(--progress-bar --output "$output")
      else
        options=(--max-time "$left" --silent --output "$output")
      fi
      rc=0
      code=$(curl -q "${transfer[@]}" "${options[@]}" \
        --write-out '%{http_code}' "$github/releases/download/$tag/$asset") || rc=$?
      if [[ $rc == 0 && $code == 200 ]]; then
        if [[ $probe == true ]]; then probe=false; continue; fi
        if [[ $display == --progress-bar ]]; then
          now=$(linux_release_uptime) || fail 'Could not read the system clock.'
          deadline=$((deadline + now - started))
        fi
        return 0
      fi
      [[ -z $pinned && $rc == 22 && $code == 404 ]] || fail "Could not download $asset."
      remaining
      if (( left <= notice_at )); then
        printf 'Waiting for %s from %s. Up to %d seconds remain. Press Ctrl-C to cancel.\n' "$asset" "$tag" "$left" >&2
        notice_at=$((left - 60))
      fi
      sleep "$((left < 15 ? left : 15))" || fail 'Interrupted while waiting for the release.'
      select_latest
      [[ $tag == "$selected" ]] || return 1
      # Poll without a progress bar until the package exists, then download it normally.
      if [[ $display == --progress-bar ]]; then probe=true; fi
    done
  }
  # The unprivileged check and protected transaction use the same package database rules.
  # Only the opened package stream crosses into the administrator's staging directory.
  IFS= read -r -d '' transaction <<'TRANSACTION' || :
    if [ "$4" = install ]; then
      PATH=/usr/sbin:/usr/bin:/sbin:/bin; export PATH
      manager_umask=$(umask)
      umask 077
      stage=$(mktemp -d /tmp/openai-system-package.XXXXXXXX)
      trap 'rm -rf -- "$stage"' EXIT
      trap 'exit 1' HUP INT TERM
      package="$stage/$1"
      cat > "$package"
      if ! printf "%s  %s\n" "$2" "$package" | sha256sum --check --status; then
        printf "%s\n" "OpenAI CLI: the staged package failed its SHA-256 check." >&2
        exit 1
      fi
      chmod 0755 "$stage"
      chmod 0644 "$package"
      umask "$manager_umask"
    else
      package=$5
    fi

    already_installed() {
      if [ "$4" = check ]; then printf "%s\n" current; exit 0; fi
      exit 76
    }
    fail() { printf 'OpenAI CLI: %s\n' "$1" >&2; exit 1; }
    if [ "$3" = rpm ]; then
      # The public release version and the verified header must agree before constructing a DNF request.
      expected=${1#openai_}
      expected=${expected%%_*}
      metadata=$(rpm -qp --queryformat '%{NAME}|%{EPOCHNUM}|%{VERSION}|%{RELEASE}|%{ARCH}|%{SHA256HEADER}' "$package")
      IFS='|' read -r name epoch version release architecture header <<RPM
$metadata
RPM
      [ "$metadata" = "$name|$epoch|$version|$release|$architecture|$header" ] &&
        [ "$name" = openai ] && [ "$version" = "$expected" ] || fail 'Unexpected RPM release identity.'
      case "$epoch" in ''|*[!0-9]*) fail 'Invalid RPM epoch.' ;; esac
      case "$release" in ''|*[!A-Za-z0-9._+~^]*) fail 'Invalid RPM release.' ;; esac
      case "$architecture" in ''|*[!A-Za-z0-9_]*) fail 'Invalid RPM architecture.' ;; esac
      case "$header" in ''|*[!0-9a-f]*) fail 'Invalid RPM header digest.' ;; esac
      [ "${#header}" -eq 64 ] || fail 'Invalid RPM header digest.'
      evr=$version-$release
      [ "$epoch" = 0 ] || evr=$epoch:$evr
    fi

    rpm_state() {
      # rpm.vercmp compares each EVR component, including on RPM before 4.16.
      # Use only the RPM database. A restrictive DNF policy can hide its metadata from the caller.
      installed=$(rpm -qa --queryformat '%{EPOCHNUM} %{VERSION} %{RELEASE} %{SHA256HEADER}\n' openai) || return 1
      if [ -z "$installed" ]; then printf 'absent\n'; return; fi
      OPENAI_INSTALLED_RPMS="$installed" OPENAI_WANTED_RPM="$epoch $version $release $header" rpm --eval '%{lua:
        local function fields(line) return assert(line:match("^(%d+) (%S+) (%S+) (%S+)$")) end
        local e, v, r, h = fields(os.getenv("OPENAI_WANTED_RPM"))
        local state = "older"
        for line in os.getenv("OPENAI_INSTALLED_RPMS"):gmatch("[^\n]+") do
          local ie, iv, ir, ih = fields(line)
          local order = rpm.vercmp(ie, e)
          if order == 0 then order = rpm.vercmp(iv, v) end
          if order == 0 then order = rpm.vercmp(ir, r) end
          if order > 0 then print("newer"); return end
          if order == 0 then state = ih == h and "equal" or "different" end
        end
        print(state)
      }'
    }
    deb_state() {
      query_status=0
      receipt=$(dpkg-query -W -f='${db:Status-Status} ${Version}' openai 2>/dev/null) || query_status=$?
      case "$query_status:$receipt" in
        1:*|0:not-installed\ *|0:config-files\ *) return 1 ;;
        0:installed\ *)
          version=$(dpkg-deb --field "$package" Version) || fail 'Could not read the verified Debian package version.'
          dpkg --compare-versions "${receipt#* }" ge "$version" ;;
        0:*) fail 'The installed Debian package needs repair.' ;;
        *) fail 'Could not read the package database.' ;;
      esac
    }
    if [ "$3" = deb ]; then
      if deb_state; then already_installed "$@"; else [ "$?" -eq 1 ] || exit 1; fi
    else
      state=$(rpm_state)
      case "$state" in
        newer|equal) already_installed "$@" ;;
        older|absent) ;;
        different) fail 'Keeping the installed system package. Its RPM header differs from the verified release.' ;;
        *) fail 'Could not compare installed RPM versions.' ;;
      esac
    fi
    [ "$4" = install ] || exit 0
    rc=0
    if [ "$3" = deb ]; then
      apt install --yes --no-remove --no-allow-downgrades "$package" || rc=$?
      if [ "$rc" -ne 0 ] && deb_state; then exit 76; fi
      exit "$rc"
    fi

    # Use a fixed relative path in the DNF command stream and stored transaction.
    # The outer trap removes this private directory and its link to the verified RPM.
    plan=$(mktemp -d "$stage/transaction.XXXXXXXX")
    mkdir -m 0700 "$plan/packages"
    ln -- "$package" "$plan/packages/verified.rpm"
    cd "$plan"
    dnf_version=$(dnf --version)
    case "$dnf_version" in
      'dnf5 version '*)
        if [ "$state" = older ]; then
          dnf --assumeyes upgrade ./packages/verified.rpm || rc=$?
        else
          # Replay an Install action so DNF rejects a concurrent downgrade.
          # Resolve dependencies against the current repository and signature policies.
          printf '{"version":"1.0","rpms":[{"nevra":"%s","action":"Install","reason":"User","repo_id":"@stored_transaction(@commandline)","package_path":"./packages/verified.rpm"}]}\n' \
            "openai-$evr.$architecture" > transaction.json
          dnf --assumeyes replay --ignore-extras . || rc=$?
          if [ "$rc" -ne 0 ] && ! dnf replay --help >/dev/null 2>&1; then
            fail 'DNF5 replay is required for a first installation. Use the Linux tarball on this system.'
          fi
        fi ;;
      4.*)
        # Load the exact package into DNF4's command-line repository before solving.
        printf 'repoquery ./packages/verified.rpm\nrepository-packages @commandline install "openai >= %s"\nrun\nexit\n' "$evr" |
          dnf --assumeyes shell || rc=$? ;;
      *) fail 'This DNF version does not support the verified local transaction.' ;;
    esac
    # DNF4 shell can return zero after a failed signature or dependency check.
    state=$(rpm_state)
    case "$state" in
      equal) [ "$rc" -eq 0 ] || exit 76 ;;
      newer) exit 76 ;;
      *) [ "$rc" -eq 0 ] || exit "$rc"; fail 'DNF did not install the verified RPM.' ;;
    esac
TRANSACTION
  while :; do
    selected=$tag
    rm -f -- "$work/checksums" "$work/package"
    name="openai_${tag#v}_linux_$arch.$format"
    download "openai_${tag#v}_checksums.txt" "$work/checksums" --silent || continue
    # The protected publisher includes this installer only for packages that support enrollment.
    digest=$(awk -v name="$name" '$2 == name || $2 == "install-linux-release.sh" {
      count[$2]++; if (NF != 2 || length($1) != 64 || $1 !~ /^[0-9a-f]+$/) bad=1
      if ($2 == name) value=$1
    } END { if (count[name] != 1 || count["install-linux-release.sh"] != 1 || bad) exit 1; print value }' "$work/checksums") ||
      fail "The release manifest lacks a unique valid checksum for $name or its supported installer."
    printf 'Downloading %s...\n' "$name" >&2
    download "$name" "$work/package" --progress-bar || continue
    printf '%s  %s\n' "$digest" "$work/package" | sha256sum --check --status ||
      fail 'The downloaded package failed its SHA-256 check.'
    installed=$( /bin/sh -eu -c "$transaction" sh "$name" "$digest" "$format" check "$work/package" ) || exit 1
    if [[ -z $pinned ]]; then select_latest; fi
    [[ $selected == "$tag" ]] && break
  done
  if [[ $installed != current ]]; then
    printf 'Verified %s. Installing the package...\n' "$name" >&2
    rc=0
    (umask "$caller_umask"; sudo /bin/sh -eu -c "$transaction" sh "$name" "$digest" "$format" install) < "$work/package" || rc=$?
    [[ $rc == 76 ]] || exit "$rc"
    installed=$( /bin/sh -eu -c "$transaction" sh "$name" "$digest" "$format" check "$work/package" ) || exit 1
    [[ $installed == current ]] || exit "$rc"
  fi

  # Check only the files the caller will use. A changed manual does not affect enrollment.
  incompatible() { fail 'Keeping the installed system package. Could not verify its automatic-update helper.'; }
  if [[ $format == deb ]]; then
    receipts=$(dpkg-query --control-show openai md5sums) || incompatible
    package_version=$(dpkg-query -W -f='${Version}' openai) || incompatible
    checksum=md5sum
  else
    receipts=$(rpm -q --queryformat '[%{FILEDIGESTS}  %{FILENAMES}\n]' openai) || incompatible
    package_version=$(rpm -q --queryformat '%{VERSION}' openai) || incompatible
    case $(rpm -q --queryformat '%{FILEDIGESTALGO}' openai) in
      8) checksum=sha256sum ;; 10) checksum=sha512sum ;; *) incompatible ;;
    esac
  fi
  for file in /usr/bin/openai /usr/share/openai-cli/install.sh /usr/share/openai-cli/enable-updates.sh; do
    [[ -n $(find "$file" -prune -type f -user 0 ! -perm -020 ! -perm -002 -size +0 -print) ]] || incompatible
    key=$file
    if [[ $format == deb ]]; then key=${file#/}; fi
    digest=$(awk -v path="$key" '$2 == path && NF == 2 {hash=$1; n++} END {if (n != 1) exit 1; print hash}' <<< "$receipts") || incompatible
    printf '%s  %s\n' "$digest" "$file" | "$checksum" --check --status || incompatible
  done
  cli=(env -i HOME="$HOME" PATH=/usr/bin:/bin OPENAI_CLI_ARCHIVE_INSTALL=1 /usr/bin/openai)
  version=$("${cli[@]}" --version) || incompatible
  release=${version#'openai version '}
  [[ $version == "openai version $release" && v$release =~ $stable ]] || incompatible
  [[ $package_version == "$release" || $format == deb && $package_version == "$release"-* ]] || incompatible
  [[ $("${cli[@]}" __openai_linux_package_starter "$release") == ready ]] || incompatible
  printf 'Keeping the installed OpenAI CLI %s and its verified automatic-update helper.\n' "$release" >&2
)

if [[ -z ${BASH_SOURCE[0]:-} || ${BASH_SOURCE[0]} == "$0" ]]; then
  PATH=/usr/sbin:/usr/bin:/sbin:/bin
  export PATH
  install_linux_release
fi
