# Interactive vector stores

Open the workspace in a terminal:

```sh
openai vector-stores
```

`openai vector-stores manage` opens the same workspace explicitly.
The workspace uses your current API key, project, organization, and endpoint settings.

1. Select a store, or choose **Create store**.
2. Choose **Upload file**.
3. Choose **Browse files** or enter a local path.
4. Select **Upload**.
5. Wait for indexing to complete.
6. Enter a question in **Search your files**.
7. Use Up and Down to preview each matching passage.
8. Press Enter to read the full passage.

Use Up and Down to select stores and store actions. Enter activates the selected item.
In a search field, Down selects the action row. Left and Right select an action.
Up returns to the field. Tab also moves between fields, actions, and results.
Enter submits the query. Escape returns from a passage to the selected result.
Escape from the results returns to the search field with your question intact.
Shift+Tab reopens the last results without another request.
Other fields select their submission action before sending a request.
Escape returns to the previous screen or cancels local work.
Ctrl+C exits and restores the terminal.

An attachment failure retains the uploaded file ID.
Retry checks the existing attachment before sending another attachment request.
If store creation loses its response, refresh the store list before retrying.
If an upload loses its response, check the project's source files before uploading again.
Stopping local work does not delete files or stop remote indexing.
Created stores have no expiry policy by default.

Choose **Manage store** for details, renaming, expiration, metadata, existing files, batches, and store deletion.
**Find store by name or ID** filters loaded stores. **Load more stores** includes another page in that search.
Store details include size, activity, expiration, file counts, and an estimate of this month's storage usage.
The estimate uses the current size across the active part of the current UTC month.
It does not calculate billing charges.

Select a completed or failed file in **Files** to inspect details, read indexed text, edit attributes, or remove its attachment.
If the API reports more indexed text, the workspace labels the displayed page as partial.
Selecting an indexing file waits for completion.
Attachment removal preserves the source file.
Store deletion preserves source files too.
Removal, deletion, and batch cancellation require the exact resource ID plus explicit confirmation.
Expired stores allow inspection and store deletion. Other mutations remain blocked.

**Attach existing files** offers a paginated file selector.
Enter toggles each file. **Attach selected** creates one file batch.
Batch actions show status and file counts, list files, refresh progress, and request processing cancellation.
The latest batch ID is prefilled in **Open file batch**.
The API does not list prior batches; other batches require a known ID.
Completed files remain attached when processing is cancelled.

**Options** supports typed attributes and custom chunking before upload or existing-file attachment.
Use a JSON object. These settings apply to the next attachment:

```json
{"attributes":{"category":"notes","revision":7,"published":true},"chunking_strategy":{"type":"static","static":{"max_chunk_size_tokens":800,"chunk_overlap_tokens":200}}}
```

Empty options use automatic chunking.
Metadata accepts string values. File attributes also accept numbers and booleans.
The workspace retains configured attachment options when recovering an interrupted upload or switching stores.
No management mutation receives an automatic retry.

Search shows matching passages and relevance scores.
It does not generate an answer.
The workspace reports additional matches when the API indicates more results.
Refine the question to narrow those results.

Existing direct commands remain available.
Explicit output options and nonterminal bare invocations retain their help behavior.
The workspace requires terminal input and output.
