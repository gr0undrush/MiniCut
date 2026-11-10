# Downloads a static ffmpeg build for Windows (gyan.dev "release essentials": x264, x265,
# NVENC, Quick Sync and AMF support) into installer\ffmpeg. Run it once before building the installer.
#   powershell -NoProfile -ExecutionPolicy Bypass -File installer\get-ffmpeg.ps1
$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$dest = Join-Path $PSScriptRoot "ffmpeg"
$need = "ffmpeg.exe", "ffprobe.exe", "ffplay.exe", "LICENSE.txt"
if (($need | Where-Object { Test-Path (Join-Path $dest $_) }).Count -eq $need.Count) {
    Write-Host "ffmpeg is already in $dest"
    exit 0
}
New-Item -ItemType Directory -Force -Path $dest | Out-Null

$url = "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip"
$tmp = Join-Path $env:TEMP ("minicut-ffmpeg-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $zip = Join-Path $tmp "ffmpeg.zip"
    Write-Host "Downloading $url ..."
    Invoke-WebRequest -Uri $url -OutFile $zip -UseBasicParsing
    Expand-Archive -Path $zip -DestinationPath $tmp -Force

    $root = Get-ChildItem -Path $tmp -Directory |
        Where-Object { Test-Path (Join-Path $_.FullName "bin\ffmpeg.exe") } |
        Select-Object -First 1
    if (-not $root) { throw "ffmpeg.exe was not found in the downloaded archive" }

    foreach ($n in "ffmpeg.exe", "ffprobe.exe", "ffplay.exe") {
        Copy-Item (Join-Path $root.FullName "bin\$n") $dest -Force
    }
    $lic = Join-Path $root.FullName "LICENSE"
    if (Test-Path $lic) {
        Copy-Item $lic (Join-Path $dest "LICENSE.txt") -Force
    } else {
        Set-Content -Path (Join-Path $dest "LICENSE.txt") -Value "FFmpeg is licensed under the GNU GPL v3 (this build). See https://ffmpeg.org/legal.html"
    }
    Write-Host "ffmpeg copied to $dest"
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
