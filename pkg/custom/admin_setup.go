package custom

import (
	"context"

	"github.com/urfave/cli/v3"
)

const adminSetupHelp = `{{$run := index .Root.Metadata "help-invocation"}}Set up an admin API key

Admin commands manage organization projects, members, and settings.
A project API key cannot run these commands.
For model requests, use {{$run}} help setup instead.

QUICK VERIFICATION
   {{$run}} setup admin
   Paste your key at the hidden prompt, then press Enter.
   The CLI verifies access automatically. The key stays in that process.
   Later commands still need authentication.

MANUAL SETUP FOR SCRIPTS OR REPEATED COMMANDS

1. OPEN THE ADMIN KEY PAGE
   https://platform.openai.com/settings/organization/admin-keys

2. SIGN IN
   Use the OpenAI Platform account for your organization.

3. SELECT YOUR ORGANIZATION
   Choose the organization you want to manage in the dashboard.
   You must be an organization owner to create an admin key.
   If you are not an owner, stop here. Ask an owner to run the command.
   Do not ask the owner to share their key.

4. CREATE AN ADMIN KEY
   Create a new admin key on that page.

5. COPY THE NEW KEY
   Copy the key when the dashboard displays it. It appears only once.
   Keep the page open until you finish the next step.

6. ENTER THE KEY IN YOUR TERMINAL
   Choose one shell below. Run each command separately.
   Paste the key only when the read command waits for input.
   Do not paste the key into a command, chat, or script.

   Bash or zsh (macOS / Linux)
   a. Run this command:
      read -rs OPENAI_ADMIN_KEY
   b. Paste the key at the waiting cursor. The terminal hides your input.
   c. Press Enter.
   d. Run this command to make the key available to the CLI:
      export OPENAI_ADMIN_KEY

   PowerShell (Windows)
   a. Run this command:
      $openaiAdminKey = Read-Host "Admin API key" -AsSecureString
   b. Paste the key at the prompt. PowerShell masks your input.
   c. Press Enter.
   d. Run this command to make the key available to the CLI:
      $env:OPENAI_ADMIN_KEY = [System.Net.NetworkCredential]::new("", $openaiAdminKey).Password
   e. Remove the temporary variable:
      Remove-Variable openaiAdminKey

   Fish: run bash first, then follow the Bash steps in that shell.
   Command Prompt (cmd.exe): run powershell first, then follow the PowerShell steps.
   Keep using that same terminal. A new terminal needs the key again.
   These commands also make the key available to programs started from this terminal.
   If you cancel key entry, stop here. Run step 9 before starting again.

7. TEST THE KEY
   Run this command in the same terminal:
      {{$run}} --format text admin organization projects list
   This command lists projects. It does not change them.
   Success: project details with ID and Name, or "No results." when the list is empty.
{{if eq $run "go run ./cmd/openai"}}   Run this example from the CLI repository directory.
{{end}}

   If the command fails:
   - "Admin API key required": repeat step 6 in this terminal.
   - HTTP 401: create a new admin key for this organization, then repeat step 6.
   - HTTP 403: ask an organization owner to check your access.
   - Connection error: check your network connection, then retry step 7.

8. RETRY YOUR ORIGINAL ADMIN COMMAND
   Run it in this same terminal. The CLI reads OPENAI_ADMIN_KEY automatically.

9. REMOVE THE KEY WHEN FINISHED
   Bash or zsh:
      unset OPENAI_ADMIN_KEY
   PowerShell:
      Remove-Item Env:OPENAI_ADMIN_KEY -ErrorAction SilentlyContinue
   This removes the key from this terminal. It does not revoke the key.

This page only shows instructions. No key has been entered or checked yet.
`

// Add the admin guide after clihelp creates the existing help/setup command.
func configureAdminSetupHelp(root *cli.Command) {
	help := root.Command("help")
	if help == nil {
		return
	}
	setup := help.Command("setup")
	if setup == nil || setup.Command("admin") != nil {
		return
	}
	setup.Commands = append(setup.Commands, &cli.Command{
		Name: "admin", Usage: "Create an admin key and test it safely", HideHelpCommand: true,
		CustomHelpTemplate: adminSetupHelp,
		Action: func(_ context.Context, command *cli.Command) error {
			if command.Args().Present() {
				return cli.Exit("Setup help takes no additional arguments.", 3)
			}
			cli.HelpPrinter(command.Root().Writer, adminSetupHelp, command)
			return nil
		},
	})
}
