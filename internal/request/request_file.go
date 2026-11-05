package request

import (
	"errors"
	"io"
	"os"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

const requestFileCapability = "openai-request-file"

// EnableRequestFile opts a reviewed command into explicit whole-request input.
func EnableRequestFile(command *cli.Command) {
	if command.Metadata == nil {
		command.Metadata = make(map[string]any)
	}
	flag := &cli.StringFlag{
		Name:      "request-file",
		Usage:     "Read a JSON or YAML request from a file, or use - for piped input.",
		OnlyOnce:  true,
		TakesFile: true,
	}
	command.Metadata[requestFileCapability] = flag
	command.Flags = append(command.Flags, flag)
}

// RequestFileFlag returns the registered whole-request input flag, if supported.
func RequestFileFlag(command *cli.Command) *cli.StringFlag {
	flag, _ := command.Metadata[requestFileCapability].(*cli.StringFlag)
	return flag
}

func readRequestFile(path string, stdin io.Reader, explicitStdin int) (map[string]any, error) {
	reader := stdin
	if IsStdinPath(path) {
		if explicitStdin != 0 {
			return nil, errors.New("--request-file - and a request parameter both use stdin")
		}
	} else {
		file, err := openRequestFile(path, explicitStdin != 0)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		reader = file
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, errors.New("cannot read --request-file input")
	}
	return requestflag.UnmarshalRequestObject(data)
}

func openRequestFile(path string, fieldUsesStdin bool) (*os.File, error) {
	cannotRead := errors.New("cannot read --request-file input")
	var expected os.FileInfo
	if fieldUsesStdin {
		// A device path can duplicate stdin, even when stdin points to a regular file.
		info, err := os.Stat(path)
		if err != nil {
			return nil, cannotRead
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("use a regular --request-file when a request parameter reads stdin")
		}
		expected = info
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, cannotRead
	}
	if expected != nil {
		opened, err := file.Stat()
		if err != nil || !os.SameFile(expected, opened) {
			file.Close()
			return nil, cannotRead
		}
	}
	return file, nil
}
