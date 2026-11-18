# Runs every check the project expects before a change is finished.
#
#   powershell -ExecutionPolicy Bypass -File scripts\test-all.ps1            # unit, integration, frontend
#   powershell -ExecutionPolicy Bypass -File scripts\test-all.ps1 -E2E       # plus Playwright

param([switch]$E2E)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$env:Path = [Environment]::GetEnvironmentVariable('Path', 'User') + ';' + [Environment]::GetEnvironmentVariable('Path', 'Machine')

function Invoke-Step([string]$name, [string]$dir, [scriptblock]$cmd) {
    Write-Host "`n==> $name" -ForegroundColor Cyan
    Push-Location (Join-Path $root $dir)
    try {
        & $cmd
        if ($LASTEXITCODE -ne 0) { throw "$name failed" }
    } finally { Pop-Location }
}

Invoke-Step 'go vet' 'backend' { go vet ./... }
# -p 2 keeps the number of concurrent test databases modest on a local PostgreSQL.
Invoke-Step 'go test' 'backend' { go test -p 2 -count=1 ./... }
Invoke-Step 'frontend lint' 'frontend' { npm run lint }
Invoke-Step 'frontend typecheck' 'frontend' { npm run typecheck }
Invoke-Step 'frontend tests' 'frontend' { npm test }
Invoke-Step 'frontend build' 'frontend' { npm run build }
if ($E2E) {
    Invoke-Step 'playwright' 'frontend' { npx playwright test }
}
Write-Host "`nAll checks passed." -ForegroundColor Green
