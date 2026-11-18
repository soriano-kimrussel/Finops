# Optional: a private PostgreSQL cluster for FinOps HQ that runs as your user on port 5433,
# so you don't need the superuser password of an existing PostgreSQL service.
#
#   powershell -ExecutionPolicy Bypass -File scripts\local-postgres.ps1 init     # first time
#   powershell -ExecutionPolicy Bypass -File scripts\local-postgres.ps1 start
#   powershell -ExecutionPolicy Bypass -File scripts\local-postgres.ps1 stop
#   powershell -ExecutionPolicy Bypass -File scripts\local-postgres.ps1 status
#
# "init" creates the cluster, starts it and runs setup-db.ps1 against it.

param([Parameter(Mandatory = $true)][ValidateSet('init', 'start', 'stop', 'status')][string]$Command)

$ErrorActionPreference = 'Stop'
$port = 5433
$base = Join-Path $env:LOCALAPPDATA 'FinOpsHQ'
$data = Join-Path $base 'pgdata'
$log = Join-Path $base 'postgres.log'
$pwFile = Join-Path $base 'superuser.txt'

$bin = Get-ChildItem 'C:\Program Files\PostgreSQL\*\bin\pg_ctl.exe' -ErrorAction SilentlyContinue |
    Sort-Object FullName -Descending | Select-Object -First 1
if (-not $bin) { throw 'PostgreSQL binaries not found under C:\Program Files\PostgreSQL.' }
$binDir = Split-Path $bin.FullName

function Start-Cluster {
    # pg_ctl keeps the console attached; launch it detached so this script returns.
    Start-Process -FilePath (Join-Path $binDir 'pg_ctl.exe') -WindowStyle Hidden -ArgumentList @(
        '-D', "`"$data`"", '-o', "`"-p $port -c listen_addresses=127.0.0.1`"", '-l', "`"$log`"", 'start')
    for ($i = 0; $i -lt 30; $i++) {
        & (Join-Path $binDir 'pg_isready.exe') -h 127.0.0.1 -p $port -q
        if ($LASTEXITCODE -eq 0) { Write-Host "PostgreSQL is running on 127.0.0.1:$port"; return }
        Start-Sleep -Milliseconds 500
    }
    throw "PostgreSQL did not become ready; see $log"
}

switch ($Command) {
    'init' {
        New-Item -ItemType Directory -Force $base | Out-Null
        if (-not (Test-Path $pwFile)) {
            $bytes = New-Object byte[] 24
            [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
            [IO.File]::WriteAllText($pwFile, (-join ($bytes | ForEach-Object { $_.ToString('x2') })))
        }
        if (-not (Test-Path (Join-Path $data 'PG_VERSION'))) {
            & (Join-Path $binDir 'initdb.exe') -D $data -U postgres --pwfile=$pwFile --auth=scram-sha-256 `
                -E UTF8 --locale-provider=builtin --builtin-locale=C.UTF-8
            if ($LASTEXITCODE -ne 0) { throw 'initdb failed' }
        }
        Start-Cluster
        $env:PGPORT = "$port"
        $env:PGPASSWORD = (Get-Content $pwFile -Raw).Trim()
        & (Join-Path $PSScriptRoot 'setup-db.ps1')
    }
    'start' { Start-Cluster }
    'stop' { & (Join-Path $binDir 'pg_ctl.exe') -D $data -m fast stop }
    'status' { & (Join-Path $binDir 'pg_ctl.exe') -D $data status }
}
