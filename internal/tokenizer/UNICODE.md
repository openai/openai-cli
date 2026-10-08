# Pinned tokenization data

These encodings match the plain-text `encode_ordinary` behavior of OpenAI tiktoken 0.14.0.
They use Unicode 16.0.0 categories, whitespace, and contraction case-fold closures.
Explicit regex classes keep results independent of the Go compiler's Unicode version.

The adapter extracts ordinary vocabulary bytes through tokenizer v0.7.0's public `Decode` API.
It builds immutable rank and fragment tables once per encoding.
It then processes exact reference pre-tokenizer matches incrementally.
Exact ranked pieces need no BPE construction.
For other pieces, pkoukk/tiktoken-go v0.1.8 performs BPE with an anchored match-all pattern.
No local BPE algorithm or arbitrary chunk boundary is introduced.

Count retains no whole-input match or token list.
Both count and inspect verify complete source-byte reconstruction before returning success.
Special-token spellings remain ordinary text.
Library initialization uses no loader API, runtime download, or persistent cache.
The existing one-MiB input limit and synchronous worst-case CPU limitation remain.

Regenerate `unicode16.go` from a local copy of the official Unicode 16.0.0 `UnicodeData.txt`.
Source: <https://www.unicode.org/Public/16.0.0/ucd/UnicodeData.txt>.
Required SHA-256: `ff58e5823bd095166564a006e47d111130813dcf8bf234ef79fa51a870edb48f`.
The generator validates that exact digest before parsing the file.
It requires no network or third-party package at generation time:

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  go run scripts/generate-tokenizer-unicode.go \
  -data /path/to/UnicodeData-16.0.0.txt
```

Run this command from the repository root with its supported Go toolchain.
The generated file identifies its source digest and generator.
The embedded `LICENSES.txt` retains the Unicode, BPE, vocabulary, and dependency notices.
Vocabulary fingerprint tests use independently generated tiktoken 0.14.0 public decoder results.
