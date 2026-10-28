#!/usr/bin/env python3
"""Capture isolated public recovery commands with synthetic local inputs."""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading

parser = argparse.ArgumentParser()
parser.add_argument("binary", type=Path)
parser.add_argument("output", type=Path)
parser.add_argument("--source", required=True)
args = parser.parse_args()
requests = []
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        requests.append(self.path)
        self.send_response(500)
        self.end_headers()
        self.wfile.write(b'{"error":{"message":"synthetic failure"}}')
    do_POST = do_GET
    def log_message(self, *_):
        pass

server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
thread = threading.Thread(target=server.serve_forever)
thread.start()
try:
    with tempfile.TemporaryDirectory(prefix="error-recovery-") as directory:
        binary = args.binary.resolve()
        command_dir = Path(directory, "bin")
        command_dir.mkdir()
        command_dir.joinpath("openai").symlink_to(binary)
        env = {"HOME":directory, "PATH":str(command_dir)+os.pathsep+"/usr/bin:/bin",
               "OPENAI_API_KEY":"synthetic-demo-key", "NO_COLOR":"1",
               "OPENAI_BASE_URL":f"http://127.0.0.1:{server.server_port}"}
        missing = str(Path(directory,"synthetic-private-missing.txt"))
        cases = [
            ("root typo", ["modles","list"], ""),
            ("nested typo", ["audio","transcriptons","create"], ""),
            ("help typo", ["help","modles","list"], ""),
            ("positional file", ["files","upload",missing,"--purpose","user_data"], ""),
            ("upload part", ["uploads","parts","create","--upload-id","synthetic-upload","--data",missing], ""),
            ("audio file", ["audio","transcribe","--model","synthetic-model","--file",missing], ""),
            ("input reference", ["responses","create","--model","synthetic-model","--input","@"+missing], ""),
            ("query reference", ["files","list","--after","@"+missing], ""),
            ("nested reference", ["chat","completions","create","--model","synthetic-model","--message",json.dumps({"role":"user","content":"@"+missing})], ""),
            ("wrong scope", ["models","list","--file",missing], ""),
            ("malformed stdin", ["responses","create","--model","synthetic-model"], "synthetic-private-input: [\n"),
            ("private suffix", ["modles","list","synthetic-private-secret\x1b[31m"], ""),
        ]
        results = []
        for name, argv, stdin in cases:
            for output_format in ["text","json"]:
                result = subprocess.run(["openai","--format-error",output_format,*argv],
                        input=stdin, text=True, capture_output=True, env=env, cwd=directory, timeout=15)
                results.append({"case":name,"format":output_format,"exit":result.returncode,
                    "stdout":result.stdout,"stderr":result.stderr})
        packet = {"source":args.source,"binary_sha256":hashlib.sha256(binary.read_bytes()).hexdigest(),
                  "requests":len(requests),"results":results}
        args.output.parent.mkdir(parents=True,exist_ok=True)
        args.output.write_text(json.dumps(packet,indent=2)+"\n")
        print(json.dumps(packet,indent=2))
finally:
    server.shutdown()
    server.server_close()
    thread.join()
