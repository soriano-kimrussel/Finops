# Starts an isolated API for the Playwright suite: recreates the finops_e2e database, then runs
# the server (with its in-process sync worker) on port 8081 in simulated QuickBooks mode.
# Playwright calls this from frontend/playwright.config.ts; you rarely need to run it by hand.

$ErrorActionPreference = 'Stop'
$backend = Join-Path (Split-Path -Parent $PSScriptRoot) 'backend'
Set-Location $backend

$machine = [Environment]::GetEnvironmentVariable('Path', 'Machine')
$user = [Environment]::GetEnvironmentVariable('Path', 'User')
$env:Path = "$user;$machine"

$lines = & go run ./cmd/e2edb
if ($LASTEXITCODE -ne 0) { throw 'could not prepare the e2e database' }
foreach ($line in $lines) {
    $name, $value = $line -split '=', 2
    Set-Item -Path "Env:$name" -Value $value
}

$env:APP_ENV = 'test'
$env:HTTP_ADDR = '127.0.0.1:8081'
$env:FRONTEND_ORIGIN = 'http://127.0.0.1:5174'
$env:QUICKBOOKS_MODE = 'simulated'
$env:WORKER_IN_PROCESS = 'true'
$env:LOG_LEVEL = 'warn'

& go build -o bin\finops-e2e.exe ./cmd/finops
if ($LASTEXITCODE -ne 0) { throw 'build failed' }
& .\bin\finops-e2e.exe serve
