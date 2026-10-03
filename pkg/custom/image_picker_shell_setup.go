package custom

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/autocomplete"
	"github.com/urfave/cli/v3"
)

// IsImagePickerShellSetupCommand recognizes explicit local setup in the root's
// remaining arguments, after global flags have been parsed. Setup validates
// its own options before changing files. API argument values never qualify.
func IsImagePickerShellSetupCommand(args []string) bool {
	if len(args) < 2 || args[0] != "@completion" {
		return false
	}
	var install, remove bool
	for index := 1; index < len(args); index++ {
		token := strings.TrimSpace(args[index])
		if token == "-" || token == "--" {
			break
		}
		if strings.HasPrefix(token, "-") && !strings.HasPrefix(token, "--") {
			first, _ := utf8.DecodeRuneInString(token[1:])
			if !unicode.IsLetter(first) {
				break
			}
		}
		name, _, hasValue := strings.Cut(token, "=")
		if !strings.HasPrefix(name, "-") {
			continue
		}
		// urfave accepts either one or two leading hyphens for full names.
		name = strings.TrimPrefix(strings.TrimPrefix(name, "-"), "-")
		if name == "profile" && !hasValue {
			index++
			continue
		}
		if name == "install-picker" || name == "uninstall-picker" {
			_, value, _ := strings.Cut(args[index], "=")
			enabled := true
			if hasValue && value != "" {
				var err error
				enabled, err = strconv.ParseBool(value)
				if err != nil {
					return false
				}
			}
			// Repeated flags use the last value, just like the command parser.
			if name == "install-picker" {
				install = enabled
			} else {
				remove = enabled
			}
		}
	}
	return install || remove
}

// Keep completion generation and persistent picker setup on the existing
// completion command, without changing the generated command definition.
func configureImagePickerShellSetup(root *cli.Command) {
	completion := root.Command("@completion")
	if completion == nil {
		return
	}
	for _, flag := range completion.Flags {
		if slices.Contains(flag.Names(), "install-picker") {
			return
		}
	}
	completion.Flags = append(completion.Flags,
		&cli.BoolFlag{Name: "install-picker", Usage: "Enable Tab shortcuts in future shell sessions"},
		&cli.BoolFlag{Name: "uninstall-picker", Usage: "Remove the managed Tab shortcut setup"},
		&cli.BoolFlag{Name: "automatic", Usage: "Installer setup for the preferred shell; preserve a previous opt-out"},
		&cli.StringFlag{Name: "profile", Usage: "Use this startup file; required for zsh unless ZDOTDIR is exported and absolute"},
	)
	next := completion.Action
	completion.Action = func(ctx context.Context, command *cli.Command) error {
		install, remove := command.Bool("install-picker"), command.Bool("uninstall-picker")
		if !install && !remove {
			if command.Bool("automatic") || command.IsSet("profile") {
				return cli.Exit("Use --install-picker or --uninstall-picker with setup options.", 2)
			}
			return next(ctx, command)
		}
		if install && remove || command.Bool("picker") || command.Args().Len() > 1 ||
			command.IsSet("profile") && command.String("profile") == "" ||
			command.Bool("automatic") && (remove || command.IsSet("profile") || command.Args().Len() > 0) {
			return cli.Exit("Choose one setup action. Automatic setup does not accept a shell or profile override.", 2)
		}
		shell := command.Args().First()
		if shell == "" && !command.Bool("automatic") {
			shell = imagePickerParentShell(ctx)
		}
		if shell == "pwsh" {
			return cli.Exit("PowerShell uses normal Tab completion. Type openai images generate and press Enter to open the image picker.", 2)
		}
		targets, err := imagePickerShellTarget(ctx, shell, command.Bool("automatic"), command.String("profile"))
		if err != nil {
			return imageSavingFailure("Could not choose a supported shell startup file. Use openai @completion SHELL --install-picker with an explicit shell and --profile PATH when needed.", err)
		}
		if len(targets) == 0 {
			return nil
		}
		if remove {
			targets = imagePickerShellRemovalTargets(targets, command.String("profile"))
		}
		keptOff := false
		decisionDirectory, err := imagePickerTabChoiceDirectory()
		if err != nil {
			return err
		}
		err = autocomplete.WithPickerSetupLock(ctx, decisionDirectory, func() error {
			migrationErr := migrateImagePickerTabDecline(ctx, string(targets[0].Shell))
			if migrationErr != nil && !remove {
				return imageSavingFailure("Could not read the Tab shortcut preference; shell setup was kept unchanged.", migrationErr)
			}
			if command.Bool("automatic") {
				declined, err := imagePickerTabDeclined(string(targets[0].Shell))
				if err != nil {
					return imageSavingFailure("Could not read the Tab shortcut preference; shell setup was kept unchanged.", err)
				}
				if declined {
					keptOff = true
					return nil
				}
			}
			var err error
			if remove {
				// Preserve the explicit opt-out even if an independent startup
				// file cannot be cleaned. Still try cleanup if saving it fails.
				err = errors.Join(migrationErr, declineImagePickerTab(ctx, string(targets[0].Shell)))
			}
			if setupErr := changeImagePickerShellSetup(ctx, targets, remove); setupErr != nil {
				return imageSavingFailure("Could not finish Tab shortcut setup. Some startup files may already be configured; rerunning this command is safe.", errors.Join(setupErr, err))
			}
			if !remove {
				err = clearImagePickerTabDecline(string(targets[0].Shell))
			}
			if err != nil {
				return imageSavingFailure("Shell setup changed, but the Tab shortcut preference could not be saved.", err)
			}
			return nil
		})
		if err != nil {
			return err
		}
		message := "Tab setup saved for future terminals. Existing custom bindings are preserved."
		if keptOff {
			message = "Tab shortcuts remain off, as requested."
		} else if remove {
			message = "Tab shortcut setup removed. Open a new terminal to finish."
		}
		_, err = fmt.Fprintln(command.Root().Writer, message)
		return err
	}
}

func changeImagePickerShellSetup(ctx context.Context, targets []autocomplete.PickerInstallation, remove bool) error {
	var failures []error
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		var err error
		if remove {
			_, err = autocomplete.RemovePicker(ctx, target)
		} else {
			_, err = autocomplete.InstallPicker(ctx, target)
		}
		if err != nil {
			if !remove {
				return err
			}
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// A private empty file records an explicit opt-out. It contains no prompt,
// executable path or credentials. Exclusive creation cannot follow a symlink.
// Consent and its decision lock must not move with configurable script storage.
func imagePickerTabChoiceDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || !imagePickerAbsolutePath(home) {
		return "", errors.New("cannot determine an absolute home directory for the Tab shortcut preference")
	}
	return filepath.Join(home, ".openai", "shell"), nil
}

func imagePickerTabChoicePath(shell string) (string, error) {
	if imagePickerShellName(shell) != shell || shell == "" {
		return "", errors.New("unsupported Tab shortcut shell")
	}
	directory, err := imagePickerTabChoiceDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "image-picker.tab-off-"+shell), nil
}

func legacyImagePickerTabChoicePath(shell string) (string, error) {
	path, err := imagePickerStatePath()
	if err != nil {
		return "", err
	}
	return path + ".tab-off-" + shell, nil
}

func imagePickerTabDeclined(shell string) (bool, error) {
	path, err := imagePickerTabChoicePath(shell)
	if err != nil {
		return false, err
	}
	declined, err := readImagePickerTabDecline(path)
	if err != nil || declined {
		return declined, err
	}
	legacy, err := legacyImagePickerTabChoicePath(shell)
	if err != nil {
		return false, err
	}
	return readImagePickerTabDecline(legacy)
}

func readImagePickerTabDecline(path string) (bool, error) {
	root, name, err := openImagePickerStateParent(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !privateImagePickerState(info) || info.Size() != 0 {
		return false, errors.New("invalid Tab shortcut preference")
	}
	return true, nil
}

// Only the current legacy configuration root is discoverable. Copy consent
// before removing its old marker, under the stable decision lock, so a later
// explicit enable cannot be undone by revisiting a migrated root.
func migrateImagePickerTabDecline(ctx context.Context, shell string) error {
	legacy, err := legacyImagePickerTabChoicePath(shell)
	if err != nil {
		return err
	}
	declined, err := readImagePickerTabDecline(legacy)
	if err != nil || !declined {
		return err
	}
	if err := declineImagePickerTab(ctx, shell); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return removeImagePickerTabDecline(legacy)
}

func declineImagePickerTab(ctx context.Context, shell string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := imagePickerTabChoicePath(shell)
	if err != nil {
		return err
	}
	root, name, err := openImagePickerStateParent(path, true)
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		_, err = readImagePickerTabDecline(path)
		return err
	}
	if err != nil {
		return err
	}
	return file.Close()
}

func clearImagePickerTabDecline(shell string) error {
	path, err := imagePickerTabChoicePath(shell)
	if err != nil {
		return err
	}
	legacy, err := legacyImagePickerTabChoicePath(shell)
	if err != nil {
		return err
	}
	for _, marker := range []string{path, legacy} {
		if _, err := readImagePickerTabDecline(marker); err != nil {
			return err
		}
	}
	return errors.Join(removeImagePickerTabDecline(path), removeImagePickerTabDecline(legacy))
}

func removeImagePickerTabDecline(path string) error {
	root, name, err := openImagePickerStateParent(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !privateImagePickerState(info) || info.Size() != 0 {
		return errors.New("invalid Tab shortcut preference")
	}
	return root.Remove(name)
}
