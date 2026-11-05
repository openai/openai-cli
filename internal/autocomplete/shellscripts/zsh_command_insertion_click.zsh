# Optional Kitty click transport. A request contains identifiers, never shell code.
# Only an active top-level zsh editor can replace its own editable command line.
if [[ ${____APPNAME___suggestions_enabled-} == 1 && -o interactive && -o zle ]]; then
  ____APPNAME___suggestions_click_token() {
    local token='' data byte index fd LC_ALL=C
    sysopen -r -o cloexec,nofollow -u fd /dev/urandom 2>/dev/null || return 1
    if ! sysread -i "$fd" -s 16 data; then
      exec {fd}<&-
      return 1
    fi
    exec {fd}<&-
    (( ${#data} == 16 )) || return 1
    for (( index=1; index<=16; ++index )); do
      printf -v byte '%02x' "'${data[index]}"
      token+=$byte
    done
    [[ ${#token} == 32 && $token != *[^0-9a-f]* ]] || return 1
    REPLY=$token
  }

  ____APPNAME___suggestions_click_owns_directory() {
    local -A info
    [[ -n ${____APPNAME___suggestions_click_directory-} &&
       ${sysparams[pid]-} == ${____APPNAME___suggestions_click_pid-} ]] || return 1
    zstat -L -H info -- "$____APPNAME___suggestions_click_directory" 2>/dev/null || return 1
    (( info[uid] == EUID && info[mode] == 8#40700 &&
      info[inode] == ____APPNAME___suggestions_click_inode &&
      info[device] == ____APPNAME___suggestions_click_device ))
  }

  ____APPNAME___suggestions_click_revoke() {
    typeset -g ____APPNAME___suggestions_click_epoch=''
    if ____APPNAME___suggestions_click_owns_directory; then
      zf_rm -f -- "$____APPNAME___suggestions_click_directory/ready" 2>/dev/null
    fi
    return 0
  }

  ____APPNAME___suggestions_click_clear() {
    ____APPNAME___suggestions_click_revoke
    typeset -g ____APPNAME___suggestions_click_generation=''
    typeset -g ____APPNAME___suggestions_click_pending=''
  }

  ____APPNAME___suggestions_click_is_owner() {
    [[ ${____APPNAME___suggestions_click_enabled-} == 1 &&
       $ZSH_SUBSHELL == 0 && ${sysparams[pid]-} == ${____APPNAME___suggestions_click_pid-} &&
       ${KITTY_WINDOW_ID-} == ${____APPNAME___suggestions_click_window-} &&
       ${KITTY_PID-} == ${____APPNAME___suggestions_click_terminal_pid-} &&
       ${TERM-} == xterm-kitty && -z ${SSH_CONNECTION-}${SSH_TTY-}${TMUX-}${STY-} ]] &&
       ____APPNAME___suggestions_click_owns_directory
  }

  ____APPNAME___suggestions_click_is_editor() {
    ____APPNAME___suggestions_click_is_owner && zle &&
      [[ -t 1 && -t 2 && ${CONTEXT-} == start && -z ${PREBUFFER-} && ${ZLE_RECURSIVE-1} == 0 &&
         $PWD == ${____APPNAME___suggestions_directory-} ]]
  }

  ____APPNAME___suggestions_click_prepare() {
    emulate -L zsh
    ____APPNAME___suggestions_click_revoke
    ____APPNAME___suggestions_click_is_owner || return 0
    [[ -t 1 || -t 2 ]] || return 0
    # ZLE widgets redirect stdin. Ordinary commands still require terminal stdin.
    [[ -t 0 ]] || zle || return 0
    local REPLY
    ____APPNAME___suggestions_click_token || return 0
    typeset -g ____APPNAME___suggestions_click_generation=$REPLY
    click_session=$____APPNAME___suggestions_click_session
    click_generation=$REPLY
  }

  ____APPNAME___suggestions_click_publish() {
    emulate -L zsh
    ____APPNAME___suggestions_click_revoke
    ____APPNAME___suggestions_click_is_editor || return 0
    (( ${#____APPNAME___suggestions} )) || return 0
    [[ -n $____APPNAME___suggestions_click_generation ]] || return 0
    local REPLY
    ____APPNAME___suggestions_click_token || return 0
    typeset -g ____APPNAME___suggestions_click_epoch=$REPLY
    # Rename publishes one complete readiness record. The parent directory is private.
    local staging=$____APPNAME___suggestions_click_directory/ready.new fd
    if ! sysopen -w -m 600 -o creat,trunc,cloexec,nofollow -u fd "$staging" 2>/dev/null; then
      ____APPNAME___suggestions_click_revoke
      return 0
    fi
    print -r -u "$fd" -- "$REPLY $____APPNAME___suggestions_click_generation $____APPNAME___suggestions_click_terminal_pid $____APPNAME___suggestions_click_window"
    local written=$?
    exec {fd}>&-
    if (( written == 0 )) && zf_mv -f -- "$staging" "$____APPNAME___suggestions_click_directory/ready" 2>/dev/null; then
      return 0
    fi
    ____APPNAME___suggestions_click_revoke
  }

  ____APPNAME___suggestions_click_request() {
    emulate -L zsh
    local chunk frame epoch generation row extra code
    # Never wait for a newline. Partial records must leave keyboard editing responsive.
    if sysread -i "$____APPNAME___suggestions_click_fd" -s 512 -t 0 chunk; then
      code=0
    else
      code=$?
    fi
    if (( code )); then
      # A timeout is harmless. Remove failed descriptors to prevent ready loops.
      (( code == 4 )) || ____APPNAME___suggestions_click_disable
      return 0
    fi
    ____APPNAME___suggestions_click_pending+=$chunk
    if (( ${#____APPNAME___suggestions_click_pending} > 512 )); then
      typeset -g ____APPNAME___suggestions_click_pending=''
      return 0
    fi
    while [[ $____APPNAME___suggestions_click_pending == *$'\n'* ]]; do
      frame=${____APPNAME___suggestions_click_pending%%$'\n'*}
      ____APPNAME___suggestions_click_pending=${____APPNAME___suggestions_click_pending#*$'\n'}
      IFS=' ' read -r epoch generation row extra <<<"$frame"
      ____APPNAME___suggestions_click_is_editor || continue
      [[ -n $____APPNAME___suggestions_click_epoch && $epoch == $____APPNAME___suggestions_click_epoch &&
         $generation == $____APPNAME___suggestions_click_generation && -z $extra ]] || continue
      [[ $row == [1-9] || $row == [1-9][0-9] || $row == 1[01][0-9] || $row == 12[0-8] ]] || continue
      # The numeric comparison only sees a bounded decimal identifier.
      [[ $frame == "$epoch $generation $row" ]] || continue
      (( row <= ${#____APPNAME___suggestions} && ! PENDING && ! KEYS_QUEUED_COUNT )) || continue
      if (( ____APPNAME___suggestions_index == 0 )) || [[ $BUFFER != $____APPNAME___suggestions_last ]]; then
        typeset -g ____APPNAME___suggestions_draft=$BUFFER ____APPNAME___suggestions_cursor=$CURSOR
      fi
      typeset -g ____APPNAME___suggestions_index=$row
      BUFFER=${____APPNAME___suggestions[$row]}
      CURSOR=${#BUFFER}
      typeset -g ____APPNAME___suggestions_last=$BUFFER
      zle -M "Command $row/${#____APPNAME___suggestions}. Enter runs the command."
      zle -R
    done
  }

  ____APPNAME___suggestions_click_disable() {
    emulate -L zsh
    typeset -g ____APPNAME___suggestions_click_enabled=0
    ____APPNAME___suggestions_click_clear
    if [[ -n ${____APPNAME___suggestions_click_fd-} ]]; then
      zle -F "$____APPNAME___suggestions_click_fd" 2>/dev/null
      exec {____APPNAME___suggestions_click_fd}>&-
    fi
    if ____APPNAME___suggestions_click_owns_directory; then
      zf_rm -f -- "$____APPNAME___suggestions_click_directory/requests" "$____APPNAME___suggestions_click_directory/ready.new" 2>/dev/null
      zf_rmdir -- "$____APPNAME___suggestions_click_directory" 2>/dev/null
    fi
    add-zle-hook-widget -d line-init ____APPNAME___suggestions_click_publish
    add-zle-hook-widget -d line-finish ____APPNAME___suggestions_click_revoke
    add-zsh-hook -d zshexit ____APPNAME___suggestions_click_disable
    typeset -g ____APPNAME___suggestions_click_directory='' ____APPNAME___suggestions_click_fd=''
  }

  () {
    emulate -L zsh
    [[ $ZSH_SUBSHELL == 0 && ${KITTY_WINDOW_ID-} == [1-9]* && ${KITTY_WINDOW_ID-} != *[^0-9]* && ${#KITTY_WINDOW_ID} -le 10 &&
       ${KITTY_PID-} == [1-9]* && ${KITTY_PID-} != *[^0-9]* && ${#KITTY_PID} -le 10 &&
       ${TERM-} == xterm-kitty && -z ${SSH_CONNECTION-}${SSH_TTY-}${TMUX-}${STY-} ]] || return 0
    zmodload zsh/system && zmodload zsh/stat &&
      zmodload -F zsh/files b:zf_rm b:zf_mv b:zf_rmdir || return 0
    autoload -Uz add-zle-hook-widget
    [[ ${____APPNAME___suggestions_click_enabled-} == 1 ]] && ____APPNAME___suggestions_click_disable
    local directory
    directory=$(umask 077; command mktemp -d "/tmp/openai-click-$EUID.XXXXXXXXXX" 2>/dev/null) || return 0
    typeset -g ____APPNAME___suggestions_click_directory=$directory
    typeset -g ____APPNAME___suggestions_click_session=${directory##*.}
    typeset -g ____APPNAME___suggestions_click_pid=$sysparams[pid]
    local -A info
    if ! zstat -L -H info -- "$directory" 2>/dev/null; then
      command rmdir -- "$directory" 2>/dev/null
      typeset -g ____APPNAME___suggestions_click_directory=''
      return 0
    fi
    typeset -g ____APPNAME___suggestions_click_inode=$info[inode] ____APPNAME___suggestions_click_device=$info[device]
    if ! (umask 077; command mkfifo -m 600 "$directory/requests") ||
        ! sysopen -rw -o nonblock,cloexec,nofollow -u ____APPNAME___suggestions_click_fd "$directory/requests"; then
      ____APPNAME___suggestions_click_disable
      return 0
    fi
    typeset -g ____APPNAME___suggestions_click_enabled=1
    typeset -g ____APPNAME___suggestions_click_window=$KITTY_WINDOW_ID
    typeset -g ____APPNAME___suggestions_click_terminal_pid=$KITTY_PID
    ____APPNAME___suggestions_click_clear
    zle -N ____APPNAME___suggestions_click_request
    zle -F -w "$____APPNAME___suggestions_click_fd" ____APPNAME___suggestions_click_request
    add-zle-hook-widget line-init ____APPNAME___suggestions_click_publish
    add-zle-hook-widget line-finish ____APPNAME___suggestions_click_revoke
    add-zsh-hook zshexit ____APPNAME___suggestions_click_disable
  }
fi
