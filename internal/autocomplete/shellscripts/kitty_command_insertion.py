"""Kitty click adapter. Sends identifiers to an active shell editor, never PTY input.

Use this kitten from an explicitly configured open-actions.conf rule:
    protocol openai-insert
    action kitten /absolute/path/openai-suggestions.py ${URL}
"""

import os
import re
import stat

from kittens.tui.handler import result_handler


_LINK = re.compile(r"openai-insert://([A-Za-z0-9]{10})/([0-9a-f]{32})/([1-9][0-9]{0,2})")
_READY = re.compile(rb"([0-9a-f]{32}) ([0-9a-f]{32}) ([1-9][0-9]{0,19}) ([1-9][0-9]{0,19})\n")


def _private(info, kind, mode, uid):
    return kind(info.st_mode) and stat.S_IMODE(info.st_mode) == mode and info.st_uid == uid


def request_insertion(uri, window_id):
    """Return whether one bounded request reached the editor's private FIFO."""
    match = _LINK.fullmatch(uri)
    if match is None or int(match[3]) > 128 or not isinstance(window_id, int) or window_id < 1:
        return False
    session, generation, identifier = match.groups()
    try:
        uid = os.getuid()
        directory = os.open(
            f"/tmp/openai-click-{uid}.{session}",
            os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_NONBLOCK,
        )
        # Each descriptor has its own cleanup scope. Outer scopes still close
        # their descriptors when validation or another descriptor's close fails.
        try:
            if not _private(os.fstat(directory), stat.S_ISDIR, 0o700, uid):
                return False
            ready = os.open("ready", os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
            try:
                info = os.fstat(ready)
                if not _private(info, stat.S_ISREG, 0o600, uid) or info.st_nlink != 1 or info.st_size > 256:
                    return False
                state = _READY.fullmatch(os.read(ready, 257))
                if (state is None or state[2].decode("ascii") != generation
                        or int(state[3]) != os.getpid() or int(state[4]) != window_id):
                    return False
                requests = os.open("requests", os.O_WRONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
                try:
                    if not _private(os.fstat(requests), stat.S_ISFIFO, 0o600, uid):
                        return False
                    # The shell rechecks this epoch and generation inside its active editor.
                    # One short write is atomic. A full FIFO or absent reader fails immediately.
                    message = state[1] + b" " + state[2] + b" " + identifier.encode("ascii") + b"\n"
                    return os.write(requests, message) == len(message)
                finally:
                    os.close(requests)
            finally:
                os.close(ready)
        finally:
            os.close(directory)
    except (OSError, AttributeError):
        return False


def main(args):
    pass


@result_handler(no_ui=True)
def handle_result(args, answer, target_window_id, boss):
    if len(args) == 2:
        request_insertion(args[1], target_window_id)
