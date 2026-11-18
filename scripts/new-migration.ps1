# Creates a pair of timestamp-versioned migration files in backend/db/migrations.
#
#   powershell -ExecutionPolicy Bypass -File scripts\new-migration.ps1 add_vendor_table
#
# The version is a UTC timestamp (YYYYMMDDHHMMSS). It is always later than the newest
# existing migration, offset by a random number of minutes and seconds.

param([Parameter(Mandatory = $true)][ValidatePattern('^[a-z0-9_]+$')][string]$Name)

$ErrorActionPreference = 'Stop'
$dir = Join-Path (Split-Path -Parent $PSScriptRoot) 'backend\db\migrations'
$format = 'yyyyMMddHHmmss'

$latest = Get-ChildItem $dir -Filter '*.up.sql' | ForEach-Object {
    if ($_.Name -match '^(\d{14})_') { [datetime]::ParseExact($Matches[1], $format, $null) }
} | Sort-Object | Select-Object -Last 1

$base = [datetime]::UtcNow
if ($latest -and $latest -ge $base) { $base = $latest }
$version = $base.AddMinutes((Get-Random -Minimum 3 -Maximum 48)).AddSeconds((Get-Random -Minimum 0 -Maximum 60)).ToString($format)

foreach ($direction in 'up', 'down') {
    $path = Join-Path $dir "${version}_$Name.$direction.sql"
    [IO.File]::WriteAllText($path, '')
    Write-Host "created $path"
}
