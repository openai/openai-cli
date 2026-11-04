# Paste into ordinary PowerShell to install the latest stable OpenAI CLI.
# A checked-out copy also accepts -ReleaseTag vX.Y.Z and -VerifyOnly.
& {
    [CmdletBinding()]
    param([string] $ReleaseTag = $env:OPENAI_CLI_WINDOWS_INSTALL_TAG, [switch] $VerifyOnly)
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'
    $repo = 'https://api.github.com/repos/openai/openai-cli'
    $workflow = 'https://github.com/openai/openai-cli/.github/workflows/publish-release.yml@refs/heads/main'
    $stable = '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
    if ($ReleaseTag -and $ReleaseTag -cnotmatch $stable) { throw 'Use a stable release tag such as v1.2.3.' }
    if ([Environment]::OSVersion.Platform -ne 'Win32NT') { throw 'Run this in ordinary Windows PowerShell or PowerShell on Windows.' }
    $user = [Security.Principal.WindowsIdentity]::GetCurrent()
    if ($user.User.Value -in 'S-1-5-18','S-1-5-19','S-1-5-20' -or ([Security.Principal.WindowsPrincipal]$user).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'Run Windows setup in a terminal without administrator privileges.'
    }
    $architecture = (Get-CimInstance Win32_Processor | Select-Object -First 1).Architecture
    $arch = switch ($architecture) { 0 { '386' } 9 { if ([Environment]::Is64BitOperatingSystem) { 'amd64' } else { '386' } } 12 { if (![Environment]::Is64BitOperatingSystem) { throw 'Unsupported Windows architecture.' }; 'arm64' } default { throw 'Unsupported Windows architecture.' } }
    $ghVersion = '2.103.3'
    $pins = @{
        '386' = @('e18001aaa03da36635626b3557947a886704d67f532292577c5945ad0535f6eb', '158ba584f8b8296858cea40f8527a46736453aac9fa6befbdffd2717d7ccb67f')
        'amd64' = @('98d0cfa794721665d7973dd728c78aff7bd836d58f10f6d7a03568eccb0a22e0', 'd303d8ac3f5e9438bf7c977b64dd6425513eae46218f8881d30694f28c1d0dfd')
        'arm64' = @('197211c98687d7770ac331fe8a3b6c198c389108c1536cdbedf659fe4703e484', 'c387d066328aadb51a8c385f7d361995589904c4fcec1b412957b033e97b3dad')
    }
    Add-Type -AssemblyName System.Net.Http
    Add-Type -AssemblyName System.IO.Compression.FileSystem

    # Fixed API origins never redirect. Only official GitHub asset URLs may
    # redirect once to the HTTPS release CDN. Keep signed URLs out of diagnostics.
    function Get-PublicResource([string]$url, [int]$limit, [string]$file) {
        $handler = [Net.Http.HttpClientHandler]::new()
        $handler.AllowAutoRedirect = $false
        $handler.UseCookies = $false
        $handler.UseDefaultCredentials = $false
        $client = [Net.Http.HttpClient]::new($handler)
        $client.MaxResponseContentBufferSize = $limit
        $client.Timeout = [TimeSpan]::FromSeconds(120)
        $client.DefaultRequestHeaders.UserAgent.ParseAdd('openai-cli-setup')
        $response = $null
        try {
            $response = $client.GetAsync($url).GetAwaiter().GetResult()
            if ($file -and [int]$response.StatusCode -in 301,302,303,307,308) {
                $redirect = $response.Headers.Location
                if (!$redirect.IsAbsoluteUri -or $redirect.Scheme -cne 'https' -or $redirect.DnsSafeHost -cne 'release-assets.githubusercontent.com' -or $redirect.Port -ne 443 -or $redirect.UserInfo) {
                    throw 'Unexpected asset origin.'
                }
                $response.Dispose()
                $response = $client.GetAsync($redirect).GetAwaiter().GetResult()
            }
            if ([int]$response.StatusCode -ne 200) { throw 'Download unavailable.' }
            if (!$file) { return ($response.Content.ReadAsStringAsync().GetAwaiter().GetResult() | ConvertFrom-Json) }
            $stream = [IO.File]::Open($file, 'CreateNew', 'Write', 'None')
            try { $response.Content.CopyToAsync($stream).GetAwaiter().GetResult() | Out-Null } finally { $stream.Dispose() }
            Set-InternetZone $file
        } catch {
            throw 'Could not download the verified release inputs. Check network access and retry.'
        } finally {
            if ($response) { $response.Dispose() }
            $client.Dispose()
        }
    }
    function Set-InternetZone([string]$path) {
        Microsoft.PowerShell.Management\Set-Content -LiteralPath $path -Stream Zone.Identifier -Encoding ASCII -NoNewline -Value "[ZoneTransfer]`r`nZoneId=3`r`n" -ErrorAction Stop
    }
    function Get-Sha([string]$path) {
        return (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
    }
    function Expand-Executable([string]$zip, [string]$member, [string]$dest) {
        $archive = [IO.Compression.ZipFile]::OpenRead($zip)
        try {
            $entries = @($archive.Entries | Where-Object { $_.FullName -ceq $member })
            if ($entries.Count -ne 1 -or $entries[0].Length -le 0 -or $entries[0].Length -gt 256MB) { throw 'Invalid ZIP executable.' }
            $inputStream = $entries[0].Open()
            try {
                $output = [IO.File]::Open($dest, 'CreateNew', 'Write', 'None')
                try {
                    $buffer = [byte[]]::new(65536)
                    while (($n = $inputStream.Read($buffer,0,$buffer.Length)) -gt 0) {
                        if ($output.Length + $n -gt $entries[0].Length) { throw 'Invalid ZIP size.' }
                        $output.Write($buffer,0,$n)
                    }
                    if ($output.Length -ne $entries[0].Length) { throw 'Incomplete ZIP executable.' }
                } finally { $output.Dispose() }
            } finally { $inputStream.Dispose() }
        } finally { $archive.Dispose() }
        Set-InternetZone $dest
    }
    function Read-Release([string]$endpoint) {
        $r = Get-PublicResource "$repo/$endpoint" 2MB
        if ($r.draft -isnot [bool] -or $r.draft -or $r.prerelease -isnot [bool] -or $r.prerelease -or
            $r.tag_name -cnotmatch $stable -or ($ReleaseTag -and $r.tag_name -cne $ReleaseTag) -or [long]$r.id -le 0 -or !$r.published_at) {
            throw 'The requested stable release is unavailable.'
        }
        $name = "openai_$($r.tag_name.Substring(1))_windows_$arch.zip"
        $assets = @($r.assets | Where-Object { $_.name -ceq $name })
        if ($assets.Count -ne 1) { throw 'The matching Windows ZIP is unavailable.' }
        $a = $assets[0]
        if ($a.digest -cnotmatch '^sha256:[0-9a-f]{64}$' -or [long]$a.size -le 0 -or [long]$a.size -gt 128MB -or [long]$a.id -le 0 -or !$a.updated_at) {
            throw 'The release lacks a usable Windows ZIP identity.'
        }
        $ref = (Get-PublicResource "$repo/git/ref/tags/$($r.tag_name)" 16384).object
        for ($i = 0; $i -lt 4 -and $ref.type -ceq 'tag' -and $ref.sha -cmatch '^[0-9a-f]{40}$'; $i++) {
            $ref = (Get-PublicResource "$repo/git/tags/$($ref.sha)" 16384).object
        }
        if ($ref.type -cne 'commit' -or $ref.sha -cnotmatch '^[0-9a-f]{40}$') { throw 'The release tag does not resolve to a commit.' }
        return [ordered]@{tag=$r.tag_name; commit=$ref.sha; releaseId=$r.id; assetId=$a.id; name=$name; size=$a.size; updated=$a.updated_at; digest=$a.digest.Substring(7)}
    }
    function Test-Publication($item, $selection) {
        $cert = $item.verificationResult.signature.certificate
        $subjects = @($item.verificationResult.statement.subject | Where-Object { $_.name -ceq $selection.name -and $_.digest.sha256 -ceq $selection.digest })
        if (!$subjects.Count -or $cert.subjectAlternativeName -cne $workflow -or $cert.sourceRepositoryDigest -cnotmatch '^[0-9a-f]{40}$' -or
            $cert.runInvocationURI -cnotmatch '^https://github\.com/openai/openai-cli/actions/runs/([1-9][0-9]*)/attempts/([1-9][0-9]*)$') { return $false }
        $runId, $attempt = $Matches[1], $Matches[2]
        $run = Get-PublicResource "$repo/actions/runs/$runId/attempts/$attempt" 2MB
        return ($run.id -eq [long]$runId -and $run.run_attempt -eq [int]$attempt -and $run.status -ceq 'completed' -and
            $run.conclusion -ceq 'success' -and $run.event -ceq 'workflow_dispatch' -and $run.head_branch -ceq 'main' -and
            $run.path -cin '.github/workflows/publish-release.yml','.github/workflows/publish-release.yml@main' -and
            $run.head_sha -ceq $cert.sourceRepositoryDigest -and $run.repository.full_name -ceq 'openai/openai-cli')
    }

    function Invoke-SetupProcess([string]$path, [string]$arguments, [bool]$verifier) {
        $start = [Diagnostics.ProcessStartInfo]::new($path,$arguments)
        $start.WorkingDirectory = $work
        $start.UseShellExecute = $false
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $verifier
        $start.EnvironmentVariables.Clear()
        $start.EnvironmentVariables['SystemRoot'] = [Environment]::GetFolderPath('Windows')
        # Both children can need the same proxy and trust settings as the parent.
        foreach ($key in 'SSL_CERT_FILE','SSL_CERT_DIR','HTTPS_PROXY','https_proxy','HTTP_PROXY','http_proxy','NO_PROXY','no_proxy') {
            $value = [Environment]::GetEnvironmentVariable($key)
            if ($value) { $start.EnvironmentVariables[$key] = $value }
        }
        if ($verifier) {
            foreach ($key in 'HOME','USERPROFILE','LOCALAPPDATA','APPDATA','TMP','TEMP','GH_CONFIG_DIR') { $start.EnvironmentVariables[$key] = $work }
            $start.EnvironmentVariables['GH_PROMPT_DISABLED'] = '1'
            $start.EnvironmentVariables['GH_NO_UPDATE_NOTIFIER'] = '1'
        } else {
            if ([IO.File]::Exists((Join-Path $work 'gh.zip'))) {
                $start.EnvironmentVariables['OPENAI_CLI_WINDOWS_ACQUIRED_VERIFIER'] = '1'
            }
            foreach ($key in 'PATH','PATHEXT','USERPROFILE','LOCALAPPDATA','APPDATA','TEMP','TMP','COMSPEC','ProgramFiles','ProgramW6432','USERNAME','USERDOMAIN') {
                $value = [Environment]::GetEnvironmentVariable($key)
                if ($value) { $start.EnvironmentVariables[$key] = $value }
            }
        }
        $process = [Diagnostics.Process]::Start($start)
        try {
            $result = $process.StandardOutput.ReadToEndAsync()
            if ($verifier) { $errors = $process.StandardError.ReadToEndAsync() }
            if (!$process.WaitForExit(150000)) { $process.Kill(); throw 'Windows setup timed out.' }
            if ($process.ExitCode -ne 0) { throw 'Release verification or Windows enrollment failed.' }
            return $result.GetAwaiter().GetResult().Trim()
        } finally { $process.Dispose() }
    }
    function Read-GoLength($reader) {
        $value = 0
        for ($shift = 0; $shift -le 21; $shift += 7) {
            $b = $reader.ReadByte()
            $value = $value -bor (($b -band 127) -shl $shift)
            if ($b -lt 128) { return $value }
        }
        throw 'Unsupported Go build information.'
    }
    function Confirm-ReleaseExecutable([string]$path, $selection) {
        # Go stores its release build settings at the start of the PE data section.
        # Bind the official publisher's actual executable to this tag's commit.
        $reader = [IO.BinaryReader]::new([IO.File]::OpenRead($path))
        try {
            $reader.BaseStream.Position = 60
            $pe = $reader.ReadUInt32()
            $reader.BaseStream.Position = $pe
            $machines = @{'386'=0x14c; amd64=0x8664; arm64=0xaa64}
            if ($reader.ReadUInt32() -ne 0x4550 -or $reader.ReadUInt16() -ne $machines[$arch]) { throw 'Unexpected Windows executable architecture.' }
            $count = $reader.ReadUInt16()
            $reader.BaseStream.Position = $pe + 20
            $table = $pe + 24 + $reader.ReadUInt16()
            for ($i = 0; $i -lt $count -and $i -lt 96; $i++) {
                $section = $table + 40*$i
                $reader.BaseStream.Position = $section
                if ([Text.Encoding]::ASCII.GetString($reader.ReadBytes(8)).TrimEnd([char]0) -cne '.data') { continue }
                $reader.BaseStream.Position = $section + 20
                $data = $reader.ReadUInt32()
                $reader.BaseStream.Position = $data
                if ($reader.ReadByte() -ne 255 -or [Text.Encoding]::ASCII.GetString($reader.ReadBytes(13)) -cne ' Go buildinf:') { break }
                $reader.BaseStream.Position = $data + 15
                if (($reader.ReadByte() -band 2) -eq 0) { break }
                $reader.BaseStream.Position = $data + 32
                $n = Read-GoLength $reader
                if ($n -gt 1024) { break }
                $reader.BaseStream.Position += $n
                $n = Read-GoLength $reader
                if ($n -gt 1MB) { break }
                $meta = [Text.Encoding]::UTF8.GetString($reader.ReadBytes($n))
                $flags = @($meta.Split([char]10) | Where-Object { $_.StartsWith("build`t-ldflags=") })
                if ($flags.Count -ne 1) { break }
                $stamp = '(?:^|\s)-X github\.com/openai/openai-cli/internal/updatenotice\.release'
                $version = [regex]::Matches($flags[0], $stamp + 'Version=([^"\s]+)')
                $commit = [regex]::Matches($flags[0], $stamp + 'Commit=([^"\s]+)')
                if ($version.Count -eq 1 -and $commit.Count -eq 1 -and $version[0].Groups[1].Value -ceq $selection.tag.Substring(1) -and
                    $commit[0].Groups[1].Value -ceq $selection.commit) { return }
                break
            }
            throw 'The executable does not match the public release version and tag commit.'
        } finally { $reader.Dispose() }
    }
    $base = [Environment]::GetFolderPath('LocalApplicationData')
    if (!$base -or [IO.DriveInfo]::new([IO.Path]::GetPathRoot($base)).DriveType -ne 'Fixed') { throw 'A local user directory is required.' }
    $sid = $user.User.Value
    $trusted = @($sid, 'S-1-5-18', 'S-1-5-32-544', 'S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464')
    for ($parent = [IO.DirectoryInfo]::new($base); $parent; $parent = $parent.Parent) {
        $acl = Get-Acl -LiteralPath $parent.FullName
        $raw = [Security.AccessControl.RawSecurityDescriptor]::new($acl.GetSecurityDescriptorBinaryForm(),0)
        if ($null -eq $raw.DiscretionaryAcl -or ($parent.Attributes -band [IO.FileAttributes]::ReparsePoint) -or $acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -notin $trusted) {
            throw 'The Windows application directory has unsafe ownership or redirection.'
        }
        foreach ($rule in $acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])) {
            # DELETE, WRITE_DAC, WRITE_OWNER, DELETE_CHILD, WRITE_EA, WRITE_ATTRIBUTES, GENERIC_WRITE, GENERIC_ALL.
            if ($rule.AccessControlType -eq 'Allow' -and !($rule.PropagationFlags -band 2) -and $rule.IdentityReference.Value -notin $trusted -and
                ([long]$rule.FileSystemRights -band 0x500D0150)) { throw 'Another account can redirect this Windows application directory.' }
        }
    }
    $work = Join-Path $base ('OpenAI-Setup-' + [Guid]::NewGuid().ToString('N'))
    $security = [Security.AccessControl.DirectorySecurity]::new()
    $security.SetSecurityDescriptorSddlForm("D:P(A;OICI;FA;;;$sid)(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
    $directory = [IO.DirectoryInfo]::new($work)
    if ($directory.Exists) { throw 'The Windows setup directory already exists.' }
    if ($PSVersionTable.PSVersion.Major -le 5) { $directory.Create($security) } else { [IO.FileSystemAclExtensions]::Create($directory,$security) }
    $directory.Refresh()
    $createdAcl = Get-Acl -LiteralPath $work
    $rules = @($createdAcl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier]))
    if (($directory.Attributes -band [IO.FileAttributes]::ReparsePoint) -or !$createdAcl.AreAccessRulesProtected -or
        $createdAcl.GetOwner([Security.Principal.SecurityIdentifier]).Value -ne $sid -or $rules.Count -ne 3 -or
        @($rules | Where-Object { $_.AccessControlType -ne 'Allow' -or $_.IdentityReference.Value -notin @($sid,'S-1-5-18','S-1-5-32-544') }).Count) {
        throw 'Could not establish a private local setup directory.'
    }
    try {
        $endpoint = if ($ReleaseTag) { "releases/tags/$ReleaseTag" } else { 'releases/latest' }
        $selected = Read-Release $endpoint
        [Console]::Error.WriteLine("Verifying OpenAI CLI $($selected.tag) for Windows $arch...")
        $verifiers = Join-Path $work 'verifier'
        $null = [IO.Directory]::CreateDirectory($verifiers)
        $gh = Join-Path $verifiers 'gh.exe'
        $installedGh = Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'GitHub CLI\gh.exe'
        $ghDirectory = [IO.Path]::GetDirectoryName($installedGh)
        $pathDirectories = @($env:Path -split ';' | ForEach-Object { $_.Trim('"').TrimEnd('\') })
        if ($pathDirectories -contains $ghDirectory -and [IO.File]::Exists($installedGh) -and (Get-Sha $installedGh) -ceq $pins[$arch][1]) {
            [IO.File]::Copy($installedGh,$gh)
        } else {
            [Console]::Error.WriteLine('Downloading the pinned GitHub CLI verifier...')
            $ghZip = Join-Path $work 'gh.zip'
            Get-PublicResource "https://github.com/cli/cli/releases/download/v$ghVersion/gh_${ghVersion}_windows_$arch.zip" 32MB $ghZip
            Set-InternetZone $ghZip
            if ((Get-Sha $ghZip) -cne $pins[$arch][0]) { throw 'The GitHub CLI archive checksum did not match.' }
            Expand-Executable $ghZip 'bin/gh.exe' $gh
        }
        Set-InternetZone $gh
        if ((Get-Sha $gh) -cne $pins[$arch][1]) { throw 'The GitHub CLI executable checksum did not match.' }
        [Console]::Error.WriteLine('Downloading the Windows ZIP...')
        $zip = Join-Path $work 'openai.zip'
        Get-PublicResource "https://github.com/openai/openai-cli/releases/download/$($selected.tag)/$($selected.name)" $selected.size $zip
        if ([IO.FileInfo]::new($zip).Length -ne $selected.size -or (Get-Sha $zip) -cne $selected.digest) { throw 'The Windows ZIP checksum did not match.' }
        $attestations = (Get-PublicResource "$repo/attestations/sha256:$($selected.digest)" 4MB).attestations
        if (!$attestations -or $attestations.Count -gt 30) { throw 'Release provenance is unavailable.' }
        $bundles = @($attestations | ForEach-Object { $_.bundle | ConvertTo-Json -Compress -Depth 100 })
        [IO.File]::WriteAllLines((Join-Path $work 'bundles.jsonl'),[string[]]$bundles,[Text.UTF8Encoding]::new($false))
        $argsText = "attestation verify openai.zip --bundle bundles.jsonl --hostname github.com --repo openai/openai-cli --cert-identity $workflow --source-ref refs/heads/main --deny-self-hosted-runners --format json"
        $verified = ConvertFrom-Json -InputObject (Invoke-SetupProcess $gh $argsText $true)
        $published = $false
        foreach ($item in $verified) { if (Test-Publication $item $selected) { $published = $true; break } }
        if (!$published) { throw 'The trusted publisher has not completed this release.' }
        if ((ConvertTo-Json -Compress $selected) -cne (ConvertTo-Json -Compress (Read-Release "releases/$($selected.releaseId)"))) {
            throw 'The public release changed during verification. Retry setup.'
        }
        $exe = Join-Path $work 'openai.exe'
        Expand-Executable $zip 'openai.exe' $exe
        Confirm-ReleaseExecutable $exe $selected
        $hash = Get-Sha $exe
        if ($VerifyOnly) {
            return ([ordered]@{tag=$selected.tag; commit=$selected.commit; assetName=$selected.name; assetSize=$selected.size; archiveSha256=$selected.digest; executableSha256=$hash; verifierSha256=$pins[$arch][1]; verifierVersion=$ghVersion} | ConvertTo-Json -Compress)
        }
        if (!(Invoke-SetupProcess $exe 'install --user' $false)) { throw 'Windows enrollment did not return an installed path.' }
        $bin = Join-Path $base 'OpenAI\CLI\bin'
        if (($env:Path -split ';',2)[0] -ine $bin) { $env:Path = "$bin;$env:Path" }
        $managed = Join-Path $bin 'openai.exe'
        $command = Microsoft.PowerShell.Core\Get-Command openai
        if ($command.CommandType -ne 'Application' -or $command.Path -ine $managed) { throw 'Another command overrides openai. Use & (Join-Path $env:LOCALAPPDATA ''OpenAI\CLI\bin\openai.exe'').' }
        return $managed
    } finally { [IO.Directory]::Delete($work,$true) }
} @args
