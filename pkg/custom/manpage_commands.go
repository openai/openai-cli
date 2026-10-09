package custom

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	docs "github.com/urfave/cli-docs/v3"
	"github.com/urfave/cli/v3"
)

func configureManpageCommands(root *cli.Command) {
	command := root.Command("@manpages")
	if command == nil || command.Action == nil {
		return
	}
	command.Action = func(_ context.Context, command *cli.Command) error {
		// cli-docs increases Markdown heading depth for each command level.
		// Deep subgroups exceed Markdown's six levels. Render a flat copy
		// with full command paths, without changing the runtime tree.
		documented := *root
		documented.Commands = manpageCommands(root.Commands, "")
		manpage, err := docs.ToManWithSection(&documented, 1)
		if err != nil {
			return err
		}

		dir := command.String("output")
		if err := os.MkdirAll(filepath.Join(dir, "man1"), 0755); err != nil {
			return err
		}
		writeFile := func(name string, compressed bool) error {
			file, err := os.Create(filepath.Join(dir, "man1", name))
			if err != nil {
				return err
			}
			return writeManpageFile(file, manpage, compressed)
		}
		if command.Bool("text") {
			if err := writeFile("openai.1", false); err != nil {
				return err
			}
		}
		if command.Bool("gzip") {
			if err := writeFile("openai.1.gz", true); err != nil {
				return err
			}
		}
		if !command.Bool("text") && !command.Bool("gzip") {
			return nil
		}
		return ReportSaveReceipt(command, os.Stderr, "Wrote manpages to "+dir, nil)
	}
}

// writeManpageFile owns file and finishes compression before closing it.
func writeManpageFile(file io.WriteCloser, manpage string, compressed bool) (err error) {
	defer func() { err = errors.Join(err, file.Close()) }()
	var written int
	if compressed {
		writer := gzip.NewWriter(file)
		defer func() { err = errors.Join(err, writer.Close()) }()
		written, err = writer.Write([]byte(manpage))
	} else {
		written, err = io.WriteString(file, manpage)
	}
	if err == nil && written != len(manpage) {
		err = io.ErrShortWrite
	}
	return err
}

func manpageCommands(commands []*cli.Command, prefix string) []*cli.Command {
	var result []*cli.Command
	for _, command := range commands {
		if command.Hidden {
			continue
		}
		copy := *command
		copy.Name = prefix + command.Name
		copy.Aliases = make([]string, len(command.Aliases))
		for i, alias := range command.Aliases {
			copy.Aliases[i] = prefix + alias
		}
		copy.Commands = nil
		result = append(result, &copy)
		result = append(result, manpageCommands(command.Commands, copy.Name+" ")...)
	}
	return result
}
