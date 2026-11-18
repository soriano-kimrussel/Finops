# Creates the FinOps HQ roles and databases on a local PostgreSQL server and writes .env.
# Safe to run more than once: existing roles get their passwords rotated to match .env.
#
#   powershell -ExecutionPolicy Bypass -File scripts\setup-db.ps1
#
# Optional: $env:PGHOST / $env:PGPORT / $env:PGSUPERUSER (default postgres).

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$envFile = Join-Path $root '.env'
$example = Join-Path $root '.env.example'

$psql = (Get-Command psql -ErrorAction SilentlyContinue).Source
if (-not $psql) {
    $candidate = Get-ChildItem 'C:\Program Files\PostgreSQL\*\bin\psql.exe' -ErrorAction SilentlyContinue |
        Sort-Object FullName -Descending | Select-Object -First 1
    if (-not $candidate) { throw 'psql.exe not found. Install PostgreSQL or add its bin folder to PATH.' }
    $psql = $candidate.FullName
}

$pgHost = if ($env:PGHOST) { $env:PGHOST } else { '127.0.0.1' }
$pgPort = if ($env:PGPORT) { $env:PGPORT } else { '5432' }
$superUser = if ($env:PGSUPERUSER) { $env:PGSUPERUSER } else { 'postgres' }

function New-Secret([int]$bytes) {
    $buf = New-Object byte[] $bytes
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($buf)
    return $buf
}
function New-HexSecret { -join ((New-Secret 24) | ForEach-Object { $_.ToString('x2') }) }

function Read-EnvFile($path) {
    $map = [ordered]@{}
    foreach ($line in Get-Content $path) {
        if ($line -match '^\s*([A-Z0-9_]+)=(.*)$') { $map[$Matches[1]] = $Matches[2] }
    }
    return $map
}

if (-not (Test-Path $envFile)) { Copy-Item $example $envFile; Write-Host 'Created .env from .env.example' }
$current = Read-EnvFile $envFile

$ownerPw = $null; $appPw = $null
if ($current['MIGRATE_DATABASE_URL'] -match '^postgres://finops_owner:([^@]+)@' -and $Matches[1] -ne 'CHANGE_ME') { $ownerPw = $Matches[1] }
if ($current['DATABASE_URL'] -match '^postgres://finops_app:([^@]+)@' -and $Matches[1] -ne 'CHANGE_ME') { $appPw = $Matches[1] }
if (-not $ownerPw) { $ownerPw = New-HexSecret }
if (-not $appPw) { $appPw = New-HexSecret }

$updates = @{
    'DATABASE_URL'         = "postgres://finops_app:$appPw@${pgHost}:$pgPort/finops_dev?sslmode=disable"
    'MIGRATE_DATABASE_URL' = "postgres://finops_owner:$ownerPw@${pgHost}:$pgPort/finops_dev?sslmode=disable"
    'TEST_DATABASE_URL'    = "postgres://finops_owner:$ownerPw@${pgHost}:$pgPort/finops_test?sslmode=disable"
    'TEST_APP_PASSWORD'    = $appPw
}
if (-not $current['TOKEN_ENCRYPTION_KEY']) { $updates['TOKEN_ENCRYPTION_KEY'] = [Convert]::ToBase64String((New-Secret 32)) }

$lines = foreach ($line in Get-Content $envFile) {
    if ($line -match '^\s*([A-Z0-9_]+)=' -and $updates.ContainsKey($Matches[1])) { "$($Matches[1])=$($updates[$Matches[1]])" } else { $line }
}
[System.IO.File]::WriteAllLines($envFile, $lines)

if (-not $env:PGPASSWORD) {
    $secure = Read-Host "Password for PostgreSQL superuser '$superUser'" -AsSecureString
    $env:PGPASSWORD = [Runtime.InteropServices.Marshal]::PtrToStringAuto([Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure))
}

$sql = @'
SELECT format('CREATE ROLE finops_owner LOGIN CREATEDB BYPASSRLS PASSWORD %L', :'owner_pw')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'finops_owner') \gexec
SELECT format('ALTER ROLE finops_owner LOGIN CREATEDB BYPASSRLS PASSWORD %L', :'owner_pw') \gexec
SELECT format('CREATE ROLE finops_app LOGIN NOBYPASSRLS PASSWORD %L', :'app_pw')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'finops_app') \gexec
SELECT format('ALTER ROLE finops_app LOGIN NOBYPASSRLS NOCREATEDB PASSWORD %L', :'app_pw') \gexec
SELECT 'CREATE DATABASE finops_dev OWNER finops_owner'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'finops_dev') \gexec
SELECT 'CREATE DATABASE finops_test OWNER finops_owner'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'finops_test') \gexec
'@

$tmp = New-TemporaryFile
try {
    Set-Content -Path $tmp -Value $sql -Encoding ascii
    & $psql -h $pgHost -p $pgPort -U $superUser -d postgres -v ON_ERROR_STOP=1 -q `
        -v "owner_pw=$ownerPw" -v "app_pw=$appPw" -f $tmp
    if ($LASTEXITCODE -ne 0) { throw "psql failed with exit code $LASTEXITCODE" }
} finally {
    Remove-Item $tmp -ErrorAction SilentlyContinue
    Remove-Item Env:PGPASSWORD -ErrorAction SilentlyContinue
}

Write-Host ''
Write-Host 'Done. Roles finops_owner / finops_app and databases finops_dev / finops_test are ready.'
Write-Host 'Next: cd backend; go run ./cmd/finops migrate up'
