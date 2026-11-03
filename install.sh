#!/bin/sh
# First-time setup from an extracted official macOS or Linux release archive.
set -eu
umask 077

fail() {
    printf '%s\n' "$1" >&2
    exit 1
}

[ "$#" -eq 0 ] || fail 'Usage: sh ./install.sh'
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
    fail 'An entry already exists at $HOME/.openai/bin/openai. Use its existing update method or select another installation.'
fi

stage=$(mktemp -d .openai-install.XXXXXXXX)
trap 'clean_stage "$stage"' EXIT
trap 'exit 1' HUP INT TERM
: > "$stage/.archive-install"
cp /dev/fd/3 "$stage/openai"
exec 3<&-
chmod 700 "$stage/openai"
version=$(env -i HOME="$HOME" PATH=/usr/bin:/bin "$stage/openai" --version) ||
    fail 'This binary could not run. Download the archive for your operating system and processor.'
case $version in
    "openai version "[0-9]*.[0-9]*.[0-9]*) ;;
    *) fail 'The archive binary did not report an OpenAI CLI release version.' ;;
esac
# Link the finished file into this directory. An occupied name always wins.
ln "$stage/openai" . || fail 'Could not install openai. Check whether another installation already exists.'
clean_stage "$stage"
trap - EXIT
printf '%s\n' 'Installed $HOME/.openai/bin/openai.' \
    'Put this installation first on PATH in Bash or zsh:' \
    '  export PATH="$HOME/.openai/bin:$PATH"' \
    '  "$HOME/.openai/bin/openai" --version' \
    '  command -v openai' \
    'If an alias or function takes precedence, use the full path above.' \
    'Automatic updates require a compatible GitHub CLI (gh) on PATH and a bare interactive openai launch.'
