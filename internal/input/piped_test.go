package input

import (
	"io"
	"os"
	"testing"
)

func TestIsPipedPreservesBorrowedFilePosition(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, data string
		offset     int64
	}{
		{"empty", "", 0},
		{"nonempty", "synthetic input", 0},
		{"nonempty at EOF", "synthetic input", int64(len("synthetic input"))},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "synthetic-input-*")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if _, err := io.WriteString(file, test.data); err != nil {
				t.Fatal(err)
			}
			if _, err := file.Seek(test.offset, io.SeekStart); err != nil {
				t.Fatal(err)
			}

			if got := IsPiped(file); got != (test.data != "") {
				t.Fatalf("classification=%v for %s", got, test.name)
			}
			data, err := io.ReadAll(file)
			if err != nil || string(data) != test.data[test.offset:] {
				t.Fatalf("probe changed file position or ownership: %q, %v", data, err)
			}
		})
	}
}

func TestIsPipedPreservesBorrowedPipeBytes(t *testing.T) {
	t.Parallel()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	const payload = "synthetic piped request"
	if _, err := io.WriteString(writer, payload); err != nil {
		t.Fatal(err)
	}
	if !IsPiped(reader) {
		t.Fatal("missed available pipe input")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != payload {
		t.Fatalf("probe changed pipe bytes or ownership: %q, %v", data, err)
	}
}

func TestIsPipedRejectsClosedFile(t *testing.T) {
	t.Parallel()

	file, err := os.CreateTemp(t.TempDir(), "synthetic-closed-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if IsPiped(file) {
		t.Fatal("reported closed file as piped input")
	}
}
