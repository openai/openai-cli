@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "ERRORLEVEL="
set "OPENAI_CLI_WINDOWS_INSTALL_LAUNCHER=1"
echo Installing OpenAI CLI for your account...
"%~dp0openai.exe" install --user
set "setup_status=%ERRORLEVEL%"
if "%setup_status%"=="86" (
  echo OpenAI CLI is installed. Review the machine PATH instruction above.
  pause
  exit /b 0
)
if "%setup_status%"=="0" (
  echo OpenAI CLI setup finished.
) else (
  echo OpenAI CLI setup failed. See the installer message above. 1>&2
  pause
)
exit /b %setup_status%
