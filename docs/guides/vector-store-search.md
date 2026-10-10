# Search vector stores

Search uses the existing vector-store API. A successful search can return no matches:

```sh
openai vector-stores search --vector-store-id vs_demo --query hello
```

```text
No matches returned.
Indexing state was not checked.
```

This message does not indicate indexing failure. Search does not make an additional status request.
Inspect processing separately before searching newly attached files:

```sh
openai vector-stores retrieve --vector-store-id vs_demo
openai vector-stores files retrieve --vector-store-id vs_demo --file-id file_demo
```

Wait for file processing to complete. A failed file exposes `last_error`; inspect it before retrying.
An empty result can also follow filters or ranking choices. Adjust those only when appropriate for your search.

```sh
openai vector-stores search --vector-store-id vs_demo --query hello \
  --filters '{"type":"eq","key":"category","value":"guide"}'
openai vector-stores search --vector-store-id vs_demo --query hello --format json
openai vector-stores search --vector-store-id vs_demo --query hello --format raw
```

Readable results retain returned scores, order, file IDs, filenames, attributes, and content chunks.
Explicit formats and field extraction retain their existing behavior. `raw` returns the API page envelope.
`--max-items 0` suppresses results. `--max-items -1` retains all items exposed by the existing search iterator.

The CLI does not provide a complete vector-store association lookup or establish deletion safety.
Do not infer that an unreferenced store is safe to delete from a partial consumer list.

Local fixtures verify CLI behavior, not live indexing acceptance.
See the [synthetic recording recipe](../../scripts/demos/vector-store-search/README.md).
