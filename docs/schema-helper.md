# Generate a schema

`openai helpers schema` makes one paid Responses request and compiles the returned schema locally.
It uses JSON mode. Model output can differ between runs.

```sh
openai helpers schema \
  --model gpt-4.1-mini-2025-04-14 \
  --description "An invoice with line items" \
  --output invoice.schema.json
```

Choose a model that supports Responses JSON mode and that your project can access.
The command requires `--model`; it has no default model or automatic fallback.
API token charges apply. See [model details](https://developers.openai.com/api/docs/models/gpt-4.1-mini) and current pricing before generation.
The command sends `store: false` and disables automatic retries.
The API can receive a request even when the client disconnects or you cancel.

`--description` takes literal text. `@` has no file meaning here.
The helper rejects piped request bodies. Use `responses create` for general JSON/YAML request construction.
`--max-output-tokens` defaults to 8192, including reasoning tokens where applicable.
Incomplete responses fail without saving a schema. Increasing this value can increase cost.

The parent directory must exist. The destination must be new; existing files and symlinks stay unchanged.
The helper stages a private file, compiles the exact returned bytes, and publishes the file with a hard link.
The filesystem must support hard links. The helper never replaces a file or silently chooses another filename.
`--output -` is unsupported. The file retains the model's JSON bytes.

Output flags apply to the save receipt:

```sh
openai --format json helpers schema --model gpt-4.1-mini-2025-04-14 \
  --description "An invoice with line items" --output invoice.schema.json
```

Normal request configuration remains available: API key, organization, project, base URL, custom headers, and mutual TLS.
Debug logging follows the existing CLI policy. Treat debug logs as sensitive.

## What validation proves

The helper uses [jsonschema/v6](https://github.com/santhosh-tekuri/jsonschema) to compile JSON Schema Draft 2020-12.
It requires a schema object, rejects duplicate JSON keys, and allows only local fragment references.
Validation never fetches a referenced schema from a file or network service.
The compiler runs in a disposable process so cancellation can stop its work.

The receipt distinguishes these results:

- JSON Schema compilation: passed.
- Structured Outputs compatibility: not checked.

[Structured Outputs](https://developers.openai.com/api/docs/guides/structured-outputs) supports a narrower subset of JSON Schema.
Successful local compilation does not prove that an API model accepts the schema or that it describes your data correctly.
The helper does not perform a second paid request to test acceptance.
Review the schema and validate representative data before using it.

Refusals, incomplete responses, malformed JSON, compiler failures, and unsupported response content fail without a success receipt.
If receipt output fails after publication, the saved file remains available.
Check the destination before repeating a paid request.

If cleanup also fails, ordinary JSON/YAML errors include the original `api_error` and a separate `cleanup_error`.
Local operation failures use `operation_error` instead of `api_error`.
Explicit `--transform-error` and raw error output preserve the original API payload and can omit cleanup details.

This is a Responses-based recipe. It does not call Playground helper endpoints or promise Playground parity.
It does not generate functions or grammars.
