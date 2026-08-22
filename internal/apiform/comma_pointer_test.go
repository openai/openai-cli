package apiform

import (
	"bytes"
	"mime/multipart"
	"strings"
	"testing"
)

func TestCommaArrayDereferencesPrimitivePointers(t *testing.T) {
	t.Parallel()

	first := "alpha"
	second := "beta"
	values := []*string{&first, nil, &second}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.SetBoundary("xxx"); err != nil {
		t.Fatalf("set boundary: %v", err)
	}

	if err := MarshalWithSettings(map[string]any{"foo": values}, writer, FormatComma); err != nil {
		t.Fatalf("encode comma-form pointer array: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	if !strings.Contains(buf.String(), "\r\nalpha,,beta\r\n") {
		t.Fatalf("multipart body did not contain dereferenced comma values: %q", buf.String())
	}
}
