"""Fetch verified archives for the minimum Unix shell checks."""
import hashlib
from pathlib import Path
import subprocess
import sys
import tempfile
import time


ARCHIVES = (
    ("bash-4.0.tar.gz", "https://ftp.gnu.org/gnu/bash/bash-4.0.tar.gz",
     "9793d394f640a95030c77d5ac989724afe196921956db741bcaf141801c50518"),
    ("zsh-5.8.tar.xz", "https://www.zsh.org/pub/old/zsh-5.8.tar.xz",
     "dcc4b54cc5565670a65581760261c163d720991f0d06486da61f8d839b52de27"),
    ("fish-4.0.0.tar.xz", "https://github.com/fish-shell/fish-shell/releases/download/4.0.0/fish-static-amd64-4.0.0.tar.xz",
     "e7bc962652763e9ca396fd81c1a6cc6105c78f4a82ec35b6bf5c3edacfbd2114"),
)
# curl transport failures: proxy/DNS/connect, partial transfer, timeout, and send/receive failures.
TRANSIENT_ERRORS = {5, 6, 7, 18, 28, 52, 55, 56}


def verified(path, expected):
    if path.is_symlink() or not path.is_file():
        return False
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest() == expected


def download(directory, name, url, expected, deadline):
    destination = directory / name
    if verified(destination, expected):
        print(f"Using verified archive: {name}", flush=True)
        return
    for attempt in range(3):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise RuntimeError(f"Archive download deadline exceeded: {name}")
        with tempfile.NamedTemporaryFile(dir=directory, prefix=f".{name}.", delete=False) as output:
            temporary = Path(output.name)
        try:
            command = ["curl", "--disable", "--fail", "--location", "--proto", "=https",
                       "--proto-redir", "=https", "--connect-timeout", "15",
                       "--max-time", f"{min(90, remaining):g}",
                       "--output", str(temporary), url]
            try:
                result = subprocess.run(command, check=False, timeout=min(95, remaining))
                code = result.returncode
            except subprocess.TimeoutExpired:
                code = 28
            if code == 0:
                if not verified(temporary, expected):
                    raise RuntimeError(f"Archive checksum mismatch: {name}")
                temporary.replace(destination)
                print(f"Verified downloaded archive: {name}", flush=True)
                return
            if code not in TRANSIENT_ERRORS or attempt == 2:
                raise RuntimeError(f"Archive download failed: {name} (curl exit {code})")
            print(f"Retrying archive download: {name} (curl exit {code})", flush=True)
        finally:
            temporary.unlink(missing_ok=True)
        time.sleep(min(2 ** attempt, max(0, deadline - time.monotonic())))


def main():
    directory = Path(sys.argv[1])
    directory.mkdir(parents=True, exist_ok=True)
    # Leave five minutes of the preparation step for compilation and package extraction.
    deadline = time.monotonic() + 180
    for archive in ARCHIVES:
        download(directory, *archive, deadline)


if __name__ == "__main__":
    main()
