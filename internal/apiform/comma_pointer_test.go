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

func TestCommaArrayPreservesFloat32PointerPrecision(t *testing.T) {
	t.Parallel()

	value := float32(1.2)
	values := []*float32{&value}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.SetBoundary("xxx"); err != nil {
		t.Fatalf("set boundary: %v", err)
	}

	if err := MarshalWithSettings(map[string]any{"foo": values}, writer, FormatComma); err != nil {
		t.Fatalf("encode comma-form float32 pointer array: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	if !strings.Contains(buf.String(), "\r\n1.2\r\n") {
		t.Fatalf("multipart body lost float32 precision semantics: %q", buf.String())
	}
}
