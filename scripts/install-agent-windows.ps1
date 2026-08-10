# Usage (Admin PowerShell):
#   .\install-agent-windows.ps1 -BinPath .\proctor-agent.exe -ServerUrl http://teacher:8911 -Student "张三" -Classroom "高一1班"
# Remote (via OpenSSH from deploy.sh):
#   powershell -NoProfile -ExecutionPolicy Bypass -File install-agent-windows.ps1 -BinPath C:\Windows\Temp\proctor-agent.exe ...
param(
  [string]$BinPath = ".\proctor-agent.exe",
  [string]$ServerUrl = "http://127.0.0.1:8911",
  [string]$Student = "",
  [string]$Classroom = "",
  [switch]$NoStart
)

$ErrorActionPreference = "Stop"
$installDir = "C:\Program Files\proctor"
$confDir = "C:\ProgramData\proctor"
$bin = Join-Path $installDir "proctor-agent.exe"
$conf = Join-Path $confDir "agent.json"

if (-not (Test-Path -LiteralPath $BinPath)) {
  throw "BinPath not found: $BinPath"
}

New-Item -ItemType Directory -Force -Path $installDir | Out-Null
New-Item -ItemType Directory -Force -Path $confDir | Out-Null

# 幂等：已安装则先停再卸（教室机重装常见）
if (Test-Path -LiteralPath $bin) {
  try { & $bin stop 2>$null } catch {}
  try { & $bin uninstall 2>$null } catch {}
}

Copy-Item -Force $BinPath $bin

@"
{
  "server_url": "$ServerUrl",
  "agent_id": "",
  "student_name": "$Student",
  "classroom": "$Classroom",
  "collect_interval_sec": 15,
  "top_n_processes": 30,
  "data_dir": "C:\\ProgramData\\proctor\\data",
  "log_file": "",
  "insecure_skip_verify": false
}
"@ | Set-Content -Encoding UTF8 $conf

& $bin install -config $conf
if (-not $NoStart) {
  & $bin start
}
& $bin status
Write-Host "Windows Service 已安装$(if (-not $NoStart) { '并启动' }) (proctor-agent)"
