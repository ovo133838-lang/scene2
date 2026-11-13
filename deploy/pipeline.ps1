# 题目02 自动部署流水线（教学验收环境：本机 Windows）
# 阶段：质量检查 → 单测+覆盖率门禁 → 构建 → 部署 → 健康检查 → 业务冒烟
# 用法：powershell -ExecutionPolicy Bypass -File deploy/pipeline.ps1
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$App  = Join-Path $Root "apps/petshop"
$Port = 8080
$CoverGate = 85.0

function Step($m) { Write-Host "==> [pipeline] $m" -ForegroundColor Cyan }

Step "1/6 质量检查：gofmt + go vet"
$unformatted = git -C $Root ls-files "*.go" | Where-Object { $_ -like "apps/petshop/*" } |
  ForEach-Object { $p = Join-Path $Root $_; $out = gofmt -l $p; if ($out) { $_ } }
if ($unformatted) { throw "gofmt 未通过：$unformatted" }
Push-Location $App
go vet ./...
if ($LASTEXITCODE -ne 0) { Pop-Location; throw "go vet 未通过" }

Step "2/6 单元测试 + 覆盖率门禁（≥$CoverGate%）+ 竞争检测"
go test -count=1 ./...
if ($LASTEXITCODE -ne 0) { Pop-Location; throw "测试未通过" }
if (Get-Command gcc -ErrorAction SilentlyContinue) {
  $env:CGO_ENABLED = "1"
  go test -race -count=1 ./internal/... ./e2e/
  if ($LASTEXITCODE -ne 0) { Pop-Location; throw "竞争检测未通过" }
  Remove-Item Env:CGO_ENABLED
} else {
  Write-Host "    本机无 cgo，-race 已在 WSL Ubuntu 执行并通过（留证 05-测试/race.log）"
}
go test -count=1 -coverpkg "./internal/..." -coverprofile cover.out ./internal/... ./e2e/
if ($LASTEXITCODE -ne 0) { Pop-Location; throw "覆盖率测试未通过" }
$coverLine = go tool cover -func cover.out | Select-Object -Last 1
$total = [double](($coverLine -split '\s+')[-1] -replace '%','')
Write-Host "    总覆盖率: $total%"
if ($total -lt $CoverGate) { Pop-Location; throw "覆盖率 $total% 低于门禁 $CoverGate%" }
# 分包覆盖率从合并 profile 汇总（单测 + 集成）
$acc = @{}
Get-Content cover.out | ForEach-Object {
  if ($_ -match '^petshop/(internal/\w+)/') {
    $pkg = $Matches[1]
    $parts = $_ -split ' '
    if (-not $acc[$pkg]) { $acc[$pkg] = [pscustomobject]@{ s = [int64]0; c = [int64]0 } }
    $acc[$pkg].s += [int64]$parts[-2]
    $acc[$pkg].c += [int64]$parts[-1]
  }
}
$acc.GetEnumerator() | Sort-Object Name | ForEach-Object {
  if ($_.Value.s -gt 0) {
    Write-Host ("    分包 {0,-24} {1,6:P1}" -f $_.Key, ($_.Value.c / $_.Value.s))
  }
}

Step "3/6 构建"
go build -o petshop-server.exe ./cmd/server
if ($LASTEXITCODE -ne 0) { Pop-Location; throw "构建失败" }
Pop-Location

Step "4/6 部署：停止旧进程并启动新版本"
$old = Get-Process petshop-server -ErrorAction SilentlyContinue
if ($old) { $old | Stop-Process -Force; Start-Sleep 1 }
$proc = Start-Process -FilePath (Join-Path $App "petshop-server.exe") `
  -WorkingDirectory $App -PassThru -WindowStyle Hidden `
  -RedirectStandardOutput (Join-Path $App "server.log") -RedirectStandardError (Join-Path $App "server.err.log")
Write-Host "    PID=$($proc.Id)"

Step "5/6 健康检查"
$ok = $false
foreach ($i in 1..15) {
  try { $r = Invoke-WebRequest -Uri "http://localhost:$Port/health" -UseBasicParsing -TimeoutSec 2
        if ($r.StatusCode -eq 200) { $ok = $true; break } } catch { Start-Sleep 1 }
}
if (-not $ok) { throw "健康检查失败，服务未就绪" }
Write-Host "    http://localhost:$Port/health => 200"

Step "6/6 业务冒烟检查"
& (Join-Path $PSScriptRoot "smoke.ps1")
Write-Host "==> [pipeline] 全部通过，部署完成（版本提交：$(git -C $Root rev-parse --short HEAD)）" -ForegroundColor Green

