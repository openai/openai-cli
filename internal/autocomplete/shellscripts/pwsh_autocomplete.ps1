Register-ArgumentCompleter -Native -CommandName __APPNAME__ -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)

  $elements = $commandAst.CommandElements
  $completionArgs = @()

  # Extract each of the arguments
  for ($i = 0; $i -lt $elements.Count; $i++) {
    $completionArgs += $elements[$i].Extent.Text
  }

  # Add empty string if there's a trailing space (wordToComplete is empty but cursor is after space)
  # Necessary for differentiating between getting completions for namespaced commands vs. subcommands
  if ($wordToComplete.Length -eq 0 -and $elements.Count -gt 0) {
    $completionArgs += ""
  }

  $output = & {
    $env:COMPLETION_STYLE = 'pwsh'
    __APPNAME__ __complete @completionArgs 2>&1
  }
  $exitCode = $LASTEXITCODE

  # Check for custom file completion patterns
  # Patterns can appear anywhere in the word (e.g., inside quotes: 'my file is @file://path')
  $prefix = ""
  $filePart = $wordToComplete
  $forceFileCompletion = $false

  # PowerShell includes quotes in $wordToComplete - strip them for pattern matching
  # but preserve them in the prefix for the completion result
  $wordContent = $wordToComplete
  $leadingQuote = ""
  if ($wordToComplete -match '^([''"])(.*)(\1)$') {
    # Fully quoted: "content" or 'content'
    $leadingQuote = $Matches[1]
    $wordContent = $Matches[2]
  } elseif ($wordToComplete -match '^([''"])(.*)$') {
    # Opening quote only: "content or 'content
    $leadingQuote = $Matches[1]
    $wordContent = $Matches[2]
  }

  if ($wordContent -match '^(.*)@(file://|data://)?(.*)$') {
    $prefix = $leadingQuote + $Matches[1] + '@' + $Matches[2]
    $filePart = $Matches[3]
    $forceFileCompletion = $true
  }

  if ($forceFileCompletion) {
    # Handle empty filePart (e.g., "@" or "@file://") by listing current directory
    $items = if ([string]::IsNullOrEmpty($filePart)) {
      Get-ChildItem -ErrorAction SilentlyContinue
    } else {
      Get-ChildItem -Path "$filePart*" -ErrorAction SilentlyContinue
    }
    $items | ForEach-Object {
      $completionText = if ($_.PSIsContainer) { $prefix + $_.Name + "/" } else { $prefix + $_.Name }
      [System.Management.Automation.CompletionResult]::new(
        $completionText,
        $completionText,
        'ProviderItem',
        $completionText
      )
    }
  } else {
    switch ($exitCode) {
      10 {
        # File completion behavior
        $items = if ([string]::IsNullOrEmpty($wordToComplete)) {
          Get-ChildItem -ErrorAction SilentlyContinue
        } else {
          Get-ChildItem -Path "$wordToComplete*" -ErrorAction SilentlyContinue
        }
        $items | ForEach-Object {
          $completionText = if ($_.PSIsContainer) { $_.Name + "/" } else { $_.Name }
          [System.Management.Automation.CompletionResult]::new(
            $completionText,
            $completionText,
            'ProviderItem',
            $completionText
          )
        }
      }
      11 {
        # No reasonable suggestions
        [System.Management.Automation.CompletionResult]::new(' ', ' ', 'ParameterValue', ' ')
      }
      default {
        # Default behavior - show command completions
        $output | ForEach-Object {
          [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
        }
      }
    }
  }
}
