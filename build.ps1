$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$dist = Join-Path $root "dist"
$env:GOCACHE = Join-Path $root ".gocache"
New-Item -ItemType Directory -Force -Path $dist | Out-Null
New-Item -ItemType Directory -Force -Path $env:GOCACHE | Out-Null

go test ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

go build -trimpath -ldflags "-H=windowsgui -s -w" -o (Join-Path $dist "TimeClicker.exe") .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "Built dist\TimeClicker.exe"
