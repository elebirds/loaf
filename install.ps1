#Requires -Version 5.1

# Install the newest public Loaf release, including release candidates.
# Set LOAF_VERSION to choose an exact release, or LOAF_INSTALL_DIR to change
# the complete package directory. LOAF_NO_MODIFY_PATH=1 leaves PATH untouched.
# LOAF_RELEASES_URL overrides the Atom feed used to discover the newest release.

function Get-LoafUserPath {
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment')
    if ($null -eq $key) { return '' }
    try {
        return [string] $key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    } finally {
        $key.Dispose()
    }
}

function Set-LoafUserPath {
    param([string] $Value)

    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
    try {
        $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
        if ($key.GetValueNames() -contains 'Path') { $kind = $key.GetValueKind('Path') }
        $key.SetValue('Path', $Value, $kind)
    } finally {
        $key.Dispose()
    }

    if (-not ('LoafInstaller.Environment' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
namespace LoafInstaller {
    public static class Environment {
        [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        public static extern IntPtr SendMessageTimeout(IntPtr window, uint message,
            UIntPtr parameter, string text, uint flags, uint timeout, out UIntPtr result);
    }
}
'@
    }
    $result = [UIntPtr]::Zero
    [void] [LoafInstaller.Environment]::SendMessageTimeout([IntPtr] 0xffff, 0x1a,
        [UIntPtr]::Zero, 'Environment', 2, 5000, [ref] $result)
}

function Add-LoafPath {
    param([string] $Directory)

    $contains = {
        param([string] $Value)
        foreach ($entry in ($Value -split ';')) {
            $expanded = [Environment]::ExpandEnvironmentVariables($entry.Trim().Trim('"'))
            if ($expanded.Replace('/', '\').TrimEnd('\') -ieq $Directory.TrimEnd('\')) { return $true }
        }
        return $false
    }
    $userPath = Get-LoafUserPath
    if (-not (& $contains $userPath)) {
        $newPath = $Directory
        if ($userPath) { $newPath += ';' + $userPath }
        Set-LoafUserPath $newPath
    }
    if (-not (& $contains $env:Path)) {
        $env:Path = $Directory + $(if ($env:Path) { ';' + $env:Path })
    }
}

& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
        throw 'Use install.sh on macOS, Linux or WSL.'
    }
    $architecture = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) { $architecture = $env:PROCESSOR_ARCHITEW6432 }
    if ($architecture -ne 'AMD64') {
        throw "Unsupported native Windows architecture: $architecture. Loaf currently provides Windows x64 only."
    }

    $installDir = $env:LOAF_INSTALL_DIR
    if (-not $installDir) { $installDir = Join-Path $env:LOCALAPPDATA 'Loaf\bin' }
    $installDir = [IO.Path]::GetFullPath($installDir)
    $releasesUrl = $env:LOAF_RELEASES_URL
    if (-not $releasesUrl) { $releasesUrl = 'https://github.com/elebirds/loaf/releases.atom' }
    $downloadUrl = $env:LOAF_DOWNLOAD_URL
    if (-not $downloadUrl) { $downloadUrl = 'https://github.com/elebirds/loaf/releases/download' }

    $temporary = Join-Path ([IO.Path]::GetTempPath()) ('loaf-install-' + [Guid]::NewGuid().ToString('N'))
    [void] (New-Item -ItemType Directory -Path $temporary)
    $previousTls = [Net.ServicePointManager]::SecurityProtocol
    try {
        [Net.ServicePointManager]::SecurityProtocol = $previousTls -bor [Net.SecurityProtocolType]::Tls12
        $version = $env:LOAF_VERSION
        if (-not $version) {
            [xml] $feed = (Invoke-WebRequest -UseBasicParsing -Uri $releasesUrl -TimeoutSec 30).Content
            $namespaces = [Xml.XmlNamespaceManager]::new($feed.NameTable)
            $namespaces.AddNamespace('atom', 'http://www.w3.org/2005/Atom')
            $entry = $feed.SelectSingleNode('/atom:feed/atom:entry[1]/atom:id', $namespaces)
            if ($null -eq $entry) { throw 'No published Loaf release was found.' }
            if ($entry.InnerText -notmatch '^tag:github\.com,2008:Repository/[0-9]+/(.+)$') {
                throw 'The Loaf release feed contains an invalid release ID.'
            }
            $version = $Matches[1]
        }
        if ($version -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$') {
            throw "Invalid Loaf release version: $version"
        }

        $archiveName = "loaf_${version}_windows_amd64.zip"
        $archive = Join-Path $temporary $archiveName
        $checksums = Join-Path $temporary 'checksums.txt'
        $base = $downloadUrl.TrimEnd('/') + '/' + $version
        Write-Host "Downloading Loaf $version (Windows x64)..."
        Invoke-WebRequest -UseBasicParsing -Uri ($base + '/' + $archiveName) -OutFile $archive -TimeoutSec 120
        Invoke-WebRequest -UseBasicParsing -Uri ($base + '/loaf_' + $version + '_checksums.txt') -OutFile $checksums -TimeoutSec 30
        $pattern = '(?m)^([a-fA-F0-9]{64})[ \t]+' + [regex]::Escape($archiveName) + '\r?$'
        $expected = [regex]::Matches([IO.File]::ReadAllText($checksums), $pattern)
        if ($expected.Count -ne 1) { throw "Missing or duplicate checksum for $archiveName." }
        $actual = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash
        if ($actual -ne $expected[0].Groups[1].Value) { throw "Checksum verification failed for $archiveName. Nothing was installed." }

        $package = Join-Path $temporary 'package'
        Expand-Archive -LiteralPath $archive -DestinationPath $package
        $source = Join-Path $package 'loaf.exe'
        if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { throw "Missing loaf.exe in $archiveName." }
        [void] (New-Item -ItemType Directory -Path $installDir -Force)
        try {
            Copy-Item -LiteralPath $source -Destination (Join-Path $installDir 'loaf.exe') -Force
        } catch {
            throw "Cannot replace $(Join-Path $installDir 'loaf.exe'). Check that the directory is writable; close Loaf or Bridge processes using this executable, then rerun. $($_.Exception.Message)"
        }
        foreach ($item in Get-ChildItem -LiteralPath $package -Force) {
            if ($item.Name -ne 'loaf.exe') {
                Copy-Item -LiteralPath $item.FullName -Destination $installDir -Recurse -Force
            }
        }
        if ($env:LOAF_NO_MODIFY_PATH -ne '1') { Add-LoafPath $installDir }
        Write-Host "Installed Loaf $version in $installDir"
        if ($env:LOAF_NO_MODIFY_PATH -eq '1') {
            Write-Host ("Run: & '{0}' version --json" -f (Join-Path $installDir 'loaf.exe').Replace("'", "''"))
        } else {
            Write-Host 'Run: loaf version --json'
        }
        Write-Host 'Native Windows requires paired MSYS2 OpenSSH and rsync; see manual/en/installation.md in the installation directory.'
    } finally {
        [Net.ServicePointManager]::SecurityProtocol = $previousTls
        Remove-Item -LiteralPath $temporary -Recurse -Force
    }
}
