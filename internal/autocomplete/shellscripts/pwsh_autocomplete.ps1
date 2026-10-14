Register-ArgumentCompleter -Native -CommandName __APPNAME__ -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)

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


  # The backend completes its final argument. Exclude the current AST element
  # and later arguments, then append PowerShell's normalized current word once.
  $completionArgs = @()
  foreach ($element in $commandAst.CommandElements) {
    if ($element.Extent.EndOffset -ge $cursorPosition) {
      break
    }
    if ($element -is [System.Management.Automation.Language.StringConstantExpressionAst]) {
      $completionArgs += $element.Value
    } else {
      $completionArgs += $element.Extent.Text
    }
  }
  $completionArgs += $wordContent

  $previousStyle = $env:COMPLETION_STYLE
  $previousFileValues = $env:OPENAI_CLI_COMPLETION_FILE_VALUES
  $previousPreserveWords = $env:OPENAI_CLI_COMPLETION_PRESERVE_WORDS
  try {
    $env:COMPLETION_STYLE = 'pwsh'
    $env:OPENAI_CLI_COMPLETION_FILE_VALUES = '1'
    $env:OPENAI_CLI_COMPLETION_PRESERVE_WORDS = '1'
    $output = __APPNAME__ __complete @completionArgs 2>&1
    $exitCode = $LASTEXITCODE
  } finally {
    $env:COMPLETION_STYLE = $previousStyle
    $env:OPENAI_CLI_COMPLETION_FILE_VALUES = $previousFileValues
    $env:OPENAI_CLI_COMPLETION_PRESERVE_WORDS = $previousPreserveWords
  }

  # Check for custom file completion patterns
  # Patterns can appear anywhere in the word (e.g., inside quotes: 'my file is @file://path')
  $prefix = ""
  $filePart = $wordToComplete
  $forceFileCompletion = $false


  if ($exitCode -ne 10 -and $wordContent -match '^(.*)@(file://|data://)?(.*)$') {
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
        # A nonempty backend result identifies an assigned file flag.
        $assignment = [string]($output | Select-Object -First 1)
        $fileValue = $wordContent
        if ($assignment.Length -gt 0) {
          $fileValue = $wordContent.Substring($assignment.Length)
        }
        # Enumerate the literal parent. Provider wildcard completion can add
        # backticks that native FileInput would treat as filename characters.
        $fileValue = $fileValue.Replace('\', [System.IO.Path]::DirectorySeparatorChar).Replace('/', [System.IO.Path]::DirectorySeparatorChar)
        $separator = $fileValue.LastIndexOf([System.IO.Path]::DirectorySeparatorChar)
        $directory = '.'
        $pathPrefix = '.' + [System.IO.Path]::DirectorySeparatorChar
        $leaf = $fileValue
        if ($separator -ge 0) {
          $directory = $fileValue.Substring(0, $separator + 1)
          $pathPrefix = $directory
          $leaf = $fileValue.Substring($separator + 1)
        } elseif (($drive = [System.IO.Path]::GetPathRoot($fileValue)).Length -gt 0) {
          # C:relative uses the drive's current directory, not C:\.
          $directory = $drive
          $pathPrefix = $drive
          $leaf = $fileValue.Substring($drive.Length)
        }
        $fileMatches = @(Get-ChildItem -LiteralPath $directory -Force -ErrorAction SilentlyContinue | ForEach-Object {
          if ($_ -is [System.IO.FileSystemInfo] -and $_.Name.StartsWith($leaf, [System.StringComparison]::OrdinalIgnoreCase)) {
            $path = $pathPrefix + $_.Name
            if ($directory.StartsWith('~')) {
              $path = $_.FullName
            }
            $type = 'ProviderItem'
            if ($_.PSIsContainer) {
              $path += [System.IO.Path]::DirectorySeparatorChar
              $type = 'ProviderContainer'
            }
            $literal = [System.Management.Automation.Language.CodeGeneration]::EscapeSingleQuotedStringContent($assignment + $path)
            [System.Management.Automation.CompletionResult]::new(
              "'$literal'", $_.Name, $type, $_.FullName
            )
          }
        })
        if ($fileMatches.Count -gt 0) {
          $fileMatches
        } else {
          # Prevent PowerShell's wildcard fallback for unmatched literal paths.
          [System.Management.Automation.CompletionResult]::new(' ', ' ', 'ParameterValue', ' ')
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
