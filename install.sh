#!/bin/sh
# First-time setup from an extracted official macOS or Linux release archive.
set -eu
umask 077

fail() {
    printf '%s\n' "$1" >&2
    exit 1
}

require_gh=false
if [ "${1:-}" = --require-gh ]; then require_gh=true; shift; fi
zsh_dir=
case $#:${1:-} in
    0:) ;;
    2:--zsh) zsh_dir=$2; [ -n "$zsh_dir" ] || fail 'Supply the directory containing .zshrc.' ;;
    *) fail 'Usage: install.sh [--require-gh] [--zsh DIRECTORY]' ;;
esac
system=$(uname -s)
case $system in
    Darwin|Linux) ;;
    *) fail 'This installer supports macOS and Linux.' ;;
esac
uid=$(id -u)
[ "$uid" != 0 ] || fail 'Run this installer as your ordinary user, without sudo.'
case ${HOME:-} in
    /*) ;;
    *) fail 'Set HOME to your absolute home directory.' ;;
esac

# Darwin ACLs can grant write access independently of the mode bits.
safe_acl() {
    [ "$system" = Darwin ] || return 0
    listing=$(LC_ALL=C /bin/ls -ldBne "$1") || return 1
    [ -n "$listing" ] || return 1
    printf '%s\n' "$listing" | LC_ALL=C /usr/bin/awk '
        NR == 1 { has_acl = ($1 ~ /[+]$/); next }
        $1 !~ /^[0-9]+:$/ || (NF != 4 && !(NF == 5 && $3 == "inherited")) { exit 1 }
        $(NF-1) == "deny" { next }
        $(NF-1) != "allow" { exit 1 }
        {
            count = split($NF, rights, ",")
            for (i = 1; i <= count; i++)
                if (rights[i] !~ /^(read|list|search|execute|readattr|readextattr|readsecurity|synchronize|file_inherit|directory_inherit|limit_inherit|only_inherit)$/)
                    exit 1
        }
        END { if (has_acl && NR == 1) exit 1 }
    '
}

# find does not follow links. Check each directory before entering it.
safe_directory() {
    [ -n "$(find "$1" -prune -type d -user "$uid" ! -perm -020 ! -perm -002 -print)" ] && safe_acl "$1"
}

CDPATH=
cd -P "$(dirname "$0")"
safe_directory . || fail 'Extract the archive into a directory only you can modify.'
[ -n "$(find ./openai -prune -type f -user "$uid" -links 1 ! -perm -020 ! -perm -002 -size +0 -print)" ] && safe_acl ./openai ||
    fail 'Extract a fresh archive in a private directory. Its openai executable must belong to you and reject writes from others.'
# Pin the checked file before leaving this directory. cp retains macOS quarantine.
exec 3< ./openai

cd -P "$HOME"
safe_directory . || fail 'Your home directory must belong to you and reject writes from other users.'
# Keep setup inside checked directories. Capture the profile identity before installing.
zsh_profile_identity() (
    cd -P "$HOME" || exit 1
    case $zsh_dir in
        "$HOME") remaining= ;;
        "$HOME"/*) remaining=${zsh_dir#"$HOME"/} ;;
        *) fail 'For zsh settings outside your home directory, add the PATH export yourself.' ;;
    esac
    while [ -n "$remaining" ]; do
        part=${remaining%%/*}
        case $part in ''|.|..) fail 'Supply a direct path to your zsh settings directory.' ;; esac
        safe_directory "./$part" || fail 'Your zsh settings directory must reject symbolic links and writes from others.'
        cd -P "./$part" || exit 1
        case $remaining in */*) remaining=${remaining#*/} ;; *) remaining= ;; esac
    done
    # The device and inode refer to the checked directory and regular file.
    # The installed CLI compares them to its open descriptors before appending.
    if [ "$system" = Darwin ]; then set -- -f '%d:%i'; else set -- -c '%d:%i'; fi
    dir_id=$(stat "$@" .) || exit 1
    file_id=new
    if [ -e .zshrc ] || [ -L .zshrc ]; then
        file_id=$(stat "$@" ./.zshrc) || exit 1
        [ -n "$(find ./.zshrc -prune -type f -user "$uid" -links 1 ! -perm -020 ! -perm -002 -print)" ] &&
            safe_acl ./.zshrc && [ -w ./.zshrc ] ||
            fail 'For a linked, shared, or read-only .zshrc, add the PATH export yourself.'
    else
        [ -w . ] || fail 'Cannot create .zshrc.'
    fi
    printf '%s/%s\n' "$dir_id" "$file_id"
)
if [ -n "$zsh_dir" ]; then profile_id=$(zsh_profile_identity); fi
for directory in .openai bin; do
    if [ ! -d "$directory" ]; then
        mkdir "$directory" 2>/dev/null || [ -d "$directory" ] || fail 'Cannot create the installation directory.'
    fi
    safe_directory "$directory" || fail 'The installation directories must belong to you and reject symlinks and writes from other users.'
    cd -P "$directory"
done
clean_stage() {
    rm -f "$1/openai" "$1/.archive-install"
    rmdir "$1" 2>/dev/null || [ ! -d "$1" ]
}
existing=false
if [ -e openai ] || [ -L openai ]; then
    # Recover a kill between link and unlink. Only remove our marked staging
    # link to this exact regular file. Never remove the installed name.
    if [ -f openai ] && [ ! -L openai ]; then
        for previous in .openai-install.????????; do
            [ -d "$previous" ] && safe_directory "$previous" || continue
            [ -f "$previous/.archive-install" ] && [ ! -L "$previous/.archive-install" ] || continue
            [ ! -L "$previous/openai" ] && [ "$previous/openai" -ef openai ] || continue
            clean_stage "$previous"
        done
    fi
    if [ -n "$zsh_dir" ] &&
       [ -n "$(find ./openai -prune -type f -user "$uid" -links 1 ! -perm -020 ! -perm -002 -print)" ] &&
       [ -x ./openai ] && safe_acl ./openai; then
        existing=true
    else
        fail 'An entry already exists at $HOME/.openai/bin/openai. Use its existing update method or select another installation.'
    fi
fi

stage=$(mktemp -d .openai-install.XXXXXXXX)
trap 'clean_stage "$stage"' EXIT
trap 'exit 1' HUP INT TERM
: > "$stage/.archive-install"
cp /dev/fd/3 "$stage/openai"
exec 3<&-
chmod 700 "$stage/openai"
if [ "$existing" = true ]; then
    cmp -s "$stage/openai" openai || fail 'A different executable already exists at $HOME/.openai/bin/openai. Keep its existing update method.'
fi
version=$(env -i HOME="$HOME" PATH=/usr/bin:/bin "$stage/openai" --version) ||
    fail 'This binary could not run. Download the archive for your operating system and processor.'
release=${version#"openai version "}
case $release in
    ''|*[!0-9.]*|.*|*.|*..*|*.*.*.*) fail 'The archive binary did not report an OpenAI CLI release version.' ;;
esac
case $version in
    "openai version "*.*.*) ;;
    *) fail 'The archive binary did not report an OpenAI CLI release version.' ;;
esac
# Check the verifier that the documented PATH will select after installation.
readiness=$(env -i HOME="$HOME" PATH="$HOME/.openai/bin:$PATH" OPENAI_CLI_ARCHIVE_INSTALL=1 "$stage/openai" __openai_archive_verifier 2>/dev/null) || readiness=unavailable
case $readiness in
    ready) advice='GitHub CLI supports update verification.' ;;
    missing)
        if [ "$system" = Darwin ] && command -v brew >/dev/null 2>&1; then
            advice='Install GitHub CLI with: brew install gh'
        else
            advice='Install GitHub CLI from https://cli.github.com/ and put gh on PATH.'
        fi ;;
    *) advice='Update GitHub CLI from https://cli.github.com/ and check which gh your PATH selects.' ;;
esac
if [ "$readiness" != ready ] && [ "$require_gh" = true ]; then
    fail "Automatic update verification is unavailable. $advice Then rerun this installer."
fi
# Link the finished file into this directory. An occupied name always wins.
if [ "$existing" = false ]; then
    ln "$stage/openai" . || fail 'Could not install openai. Check whether another installation already exists.'
fi
check_destination() {
    if [ "$existing" = false ]; then
        [ ! -L openai ] && [ openai -ef "$stage/openai" ]
    else
        [ -n "$(find ./openai -prune -type f -user "$uid" -links 1 ! -perm -020 ! -perm -002 -print)" ] &&
            safe_acl ./openai && cmp -s "$stage/openai" ./openai
    fi
}
check_destination || fail 'The installed executable changed. Check which copy you want before enabling updates.'
if [ -n "$zsh_dir" ]; then
    selected_cli=$(pwd -P)/$stage/openai
    profile_result=$(cd -P "$zsh_dir" && env -i HOME="$HOME" PATH=/usr/bin:/bin OPENAI_CLI_ARCHIVE_INSTALL=1 \
        "$selected_cli" __openai_archive_zsh_profile "$zsh_dir" "$profile_id") || profile_result=
    [ "$profile_result" = ready ] || fail 'The CLI is installed. Check your zsh profile and rerun setup.'
    check_destination || fail 'The installed executable changed during setup. Check the selected copy.'
fi
clean_stage "$stage"
trap - EXIT
printf 'Installed OpenAI CLI %s at $HOME/.openai/bin/openai.\n' "$release"
if [ -z "$zsh_dir" ] && [ "$(command -v openai || :)" != "$HOME/.openai/bin/openai" ]; then
    printf '%s\n' 'Select it in this shell: export PATH="$HOME/.openai/bin:$PATH"'
fi
printf '%s\n' "$advice"
if [ "$readiness" = ready ]; then
    printf '%s\n' 'The CLI checks for updates after eligible interactive API requests or bare openai.'
else
    printf '%s\n' 'The CLI is installed. After fixing gh, use openai in a terminal to resume update checks.'
fi
