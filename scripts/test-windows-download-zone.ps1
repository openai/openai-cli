# Read the full NTFS stream. Managed downloads use canonical ASCII attachment marks.
function Get-WindowsDownloadZone([string]$path) {
    $mode = if ($PSVersionTable.PSVersion.Major -le 5) {@{Encoding='Byte'; ReadCount=0}} else {@{AsByteStream=$true; Raw=$true}}
    $bytes = [byte[]](Microsoft.PowerShell.Management\Get-Content -LiteralPath $path -Stream Zone.Identifier @mode -ErrorAction Stop)
    $text = [Text.Encoding]::ASCII.GetString($bytes)
    foreach ($id in 3,4) {
        $expected = "[ZoneTransfer]`r`nZoneId=$id`r`n"
        if ($bytes.Length -eq $expected.Length -and [string]::Equals($text,$expected,[StringComparison]::Ordinal)) {return $id}
    }
    return 0
}
