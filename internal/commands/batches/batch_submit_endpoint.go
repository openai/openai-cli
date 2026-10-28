package batches

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/openai/openai-go/v3"
)

var batchSubmitEndpoints = [...]openai.BatchNewParamsEndpoint{
	openai.BatchNewParamsEndpointV1Responses,
	openai.BatchNewParamsEndpointV1ChatCompletions,
	openai.BatchNewParamsEndpointV1Embeddings,
	openai.BatchNewParamsEndpointV1Completions,
	openai.BatchNewParamsEndpointV1Moderations,
	openai.BatchNewParamsEndpointV1ImagesGenerations,
	openai.BatchNewParamsEndpointV1ImagesEdits,
	openai.BatchNewParamsEndpointV1Videos,
}

// inferBatchSubmitEndpoint inspects the private bytes that submission uploads.
func inferBatchSubmitEndpoint(ctx context.Context, path string) (endpoint string, snapshot *batchSubmissionSnapshot, err error) {
	snapshot, err = prepareBatchSubmissionSnapshot(ctx, path)
	if err != nil {
		return "", nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, snapshot.close())
			endpoint, snapshot = "", nil
		}
	}()
	endpoint, err = inferBatchSubmitJSONL(ctx, batchRecordContextReader{ctx, io.NewSectionReader(snapshot.file, 0, snapshot.Size())})
	return endpoint, snapshot, err
}

func inferBatchSubmitJSONL(ctx context.Context, reader io.Reader) (string, error) {
	allowed := make(map[string]bool, len(batchSubmitEndpoints))
	capture := 0
	for _, endpoint := range batchSubmitEndpoints {
		allowed[string(endpoint)] = true
		// Every ASCII character may use a six-byte JSON Unicode escape.
		capture = max(capture, 6*len(endpoint)+2)
	}
	scanner := batchJSONScanner{ctx: ctx, in: bufio.NewReader(reader), out: bufio.NewWriter(io.Discard), urlCapture: capture}
	endpoint := ""
	for line := 1; ; line++ {
		err := scanner.outerSpace()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		b, err := scanner.peek()
		if err != nil {
			return "", err
		}
		if b == '\n' {
			if _, err := scanner.take(false); err != nil {
				return "", err
			}
			continue
		}
		scanner.idStart, scanner.idLength, scanner.idFields = 0, 0, 0
		scanner.url, scanner.urlFields = "", 0
		if b != '{' {
			err = errBatchRecordJSON
		} else {
			err = scanner.value(0)
		}
		switch {
		case errors.Is(err, errBatchRecordDuplicateURL):
			return "", batchSubmitEndpointLineError(line, "repeats url", "Keep one top-level url per request, then retry.")
		case errors.Is(err, errBatchRecordURL):
			return "", batchSubmitEndpointLineError(line, "has a non-string url", "Set url to the request endpoint, then retry.")
		case errors.Is(err, errBatchRecordJSON), errors.Is(err, errBatchRecordID), err == io.EOF:
			return "", batchSubmitEndpointLineError(line, "is not a valid JSON request object", "Fix the JSONL file, then retry.")
		case err != nil:
			return "", err
		}
		if scanner.urlFields == 0 {
			return "", batchSubmitEndpointLineError(line, "has no top-level url", "Add the request endpoint as url, then retry.")
		}
		if !allowed[scanner.url] {
			return "", batchSubmitEndpointLineError(line, "has an unsupported url", "Check the request endpoint, or provide --endpoint explicitly for a custom endpoint.")
		}
		if endpoint != "" && endpoint != scanner.url {
			return "", batchSubmitEndpointLineError(line, "uses a different endpoint", "Split requests into separate files, with one endpoint per batch.")
		}
		endpoint = scanner.url
		err = scanner.outerSpace()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		b, err = scanner.take(false)
		if err != nil {
			return "", err
		}
		if b != '\n' {
			return "", batchSubmitEndpointLineError(line, "has content after its request object", "Put each JSON request on its own line, then retry.")
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if endpoint == "" {
		return "", batchError("Cannot determine the batch endpoint: the input has no requests. Add JSONL requests, then retry.")
	}
	return endpoint, nil
}

func batchSubmitEndpointLineError(line int, problem, recovery string) error {
	return batchError(fmt.Sprintf("Cannot determine the batch endpoint: line %d %s. %s", line, problem, recovery))
}
