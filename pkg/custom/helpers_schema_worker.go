package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

const schemaValidationArgument = "__openai-schema-validate"

const schemaValidAcknowledgement = "openai-schema-valid-v1\n"
const schemaInvalidAcknowledgement = "openai-schema-invalid-v1\n"

var errHelperSchemaInvalid = errors.New("invalid schema artifact")

// RunSchemaValidationHelper runs before CLI setup. This child never loads
// credentials, invokes an API, writes an artifact, or starts another process.
func RunSchemaValidationHelper(args []string, input io.Reader, output io.Writer) (bool, int) {
	if len(args) != 2 || args[1] != schemaValidationArgument {
		return false, 0
	}
	data, err := io.ReadAll(input)
	if err != nil {
		return true, 1
	}
	if err := validateHelperSchema(context.Background(), data); err != nil {
		if _, err := io.WriteString(output, schemaInvalidAcknowledgement); err != nil {
			return true, 1
		}
		return true, 2
	}
	if _, err := io.WriteString(output, schemaValidAcknowledgement); err != nil {
		return true, 1
	}
	return true, 0
}

func compileSchemaArtifact(ctx context.Context, data []byte) error {
	executable, err := os.Executable()
	if err != nil {
		return schemaCompilerLocalFailure(err)
	}
	return runSchemaCompiler(ctx, executable, data)
}

// The compiler has no cancellation API. A disposable process permits stopping
// all compiler work without payload caps or abandoned goroutines.
func runSchemaCompiler(ctx context.Context, executable string, data []byte) error {
	command := exec.CommandContext(ctx, executable, schemaValidationArgument)
	command.Env = []string{}
	// Windows process startup can require SystemRoot. No request environment is inherited.
	if systemRoot, ok := os.LookupEnv("SystemRoot"); ok {
		command.Env = append(command.Env, "SystemRoot="+systemRoot)
	}
	command.WaitDelay = 250 * time.Millisecond
	command.Stdin = bytes.NewReader(data)
	var acknowledgement schemaCompilerAcknowledgement
	command.Stdout = &acknowledgement
	command.Stderr = io.Discard
	err := command.Run()
	if ctx.Err() != nil {
		return errors.Join(err, ctx.Err())
	}
	if err == nil && acknowledgement.String() == schemaValidAcknowledgement {
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 2 && acknowledgement.String() == schemaInvalidAcknowledgement {
		return errHelperSchemaInvalid
	}
	return schemaCompilerLocalFailure(err)
}

func schemaCompilerLocalFailure(cause error) error {
	return &schemaHelperError{message: "Local schema compiler could not complete validation. No file was saved. Check the CLI installation and available memory before retrying.", cause: cause}
}

// Bound protocol output from a replaced or incompatible executable.
type schemaCompilerAcknowledgement struct{ buffer bytes.Buffer }

func (a *schemaCompilerAcknowledgement) String() string { return a.buffer.String() }

func (a *schemaCompilerAcknowledgement) Write(data []byte) (int, error) {
	if len(data) > 64-a.buffer.Len() {
		return 0, errors.New("unexpected schema compiler output")
	}
	return a.buffer.Write(data)
}
