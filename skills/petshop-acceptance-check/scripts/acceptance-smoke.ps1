# 验收探针：对运行中的系统做端到端验收抽测
param(
  [string]$Base = "http://localhost:8080",
  [string]$Db = "petshop_t02",
  [string]$MysqlExe = "C:\Program Files\MySQL\MySQL Server 8.0\bin\mysql.exe"
)
$ErrorActionPreference = "Stop"
$env:MYSQL_PWD = "root"
$script:Fails = 0
function Check($no, $name, $cond, $detail) {
  if ($cond) { Write-Output ("[PASS] {0} {1}" -f $no, $name) }
  else { Write-Output ("[FAIL] {0} {1} ({2})" -f $no, $name, $detail); $script:Fails++ }
}
function Sql([string]$q) {
  & $MysqlExe -uroot --host=127.0.0.1 --port=3306 -N -B -e $q $Db 2>$null
}

Write-Output "=== 题目02 验收探针（$Base，库：$Db）==="

# A1 健康
try { $h = Invoke-WebRequest "$Base/health" -UseBasicParsing -TimeoutSec 5 } catch { $h = $null }
Check "A1" "健康检查" ($h -and $h.StatusCode -eq 200) "health"

# A2 登录与角色页面
$s = New-Object Microsoft.PowerShell.Commands.WebRequestSession
Invoke-WebRequest "$Base/login" -Method Post -Body @{username="u1";password="demo123"} `
  -WebSession $s -UseBasicParsing | Out-Null
$p = Invoke-WebRequest "$Base/user" -WebSession $s -UseBasicParsing
Check "A2" "用户登录与用户页" ($p.Content -match "可约时段") "user page"

# A3 预约→支付→确认（核心流）
$slots = [regex]::Matches($p.Content, 'name="slotId" value="(\d+)"')
if ($slots.Count -eq 0) { Check "A3" "预约支付核心流" $false "无可约时段"; return }
$sid = $slots[$slots.Count - 1].Groups[1].Value
Invoke-WebRequest "$Base/user/book" -Method Post -Body @{petId="1";slotId=$sid} `
  -WebSession $s -MaximumRedirection 0 -UseBasicParsing -ErrorAction SilentlyContinue | Out-Null
$p2 = Invoke-WebRequest "$Base/user" -WebSession $s -UseBasicParsing
$bid = [regex]::Match($p2.Content, 'name="bookingId" value="(B\w+)"').Groups[1].Value
Check "A3a" "预约成功待付款" ($bid -ne "" -and $p2.Content -match "待付款") $bid
Invoke-WebRequest "$Base/user/pay" -Method Post -Body @{bookingId=$bid} `
  -WebSession $s -MaximumRedirection 0 -UseBasicParsing -ErrorAction SilentlyContinue | Out-Null
$mock = Invoke-WebRequest "$Base/mock" -UseBasicParsing
$payId = ""
foreach ($row in ($mock.Content -split "<tr")) {
  if ($row -match [regex]::Escape($bid) -and $row -match 'value="(P\w+)"') { $payId = $Matches[1]; break }
}
if ($payId -ne "") {
  Invoke-WebRequest "$Base/mock/pay" -Method Post -Body @{paymentId=$payId;action="success"} `
    -UseBasicParsing | Out-Null
}
$p3 = Invoke-WebRequest "$Base/user" -WebSession $s -UseBasicParsing
$row = ($p3.Content -split "<tr" | Where-Object { $_ -match [regex]::Escape($bid) }) -join ""
Check "A3b" "支付成功→已确认" ($row -match "已确认") $bid

# A4 数据一致性（调用 petshop-booking-check 的核查脚本）
$skillsRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSCommandPath))
$chk = & (Join-Path $skillsRoot "petshop-booking-check\scripts\check-business.ps1") -Db $Db -MysqlExe $MysqlExe
$chk | ForEach-Object { Write-Output ("  [skill] " + $_) }
Check "A4" "业务约束一致性（petshop-booking-check）" (-not ($chk | Where-Object { $_ -like "[FAIL]*" })) "sql checks"

# A5 幂等抽检：重复支付通知
if ($payId -ne "") {
  $cb2 = Invoke-WebRequest "$Base/mock/pay" -Method Post -Body @{paymentId=$payId;action="resend"} `
    -UseBasicParsing -ErrorAction SilentlyContinue
  Check "A5" "重复支付通知幂等" ($cb2 -and $cb2.Content -match "duplicate") "resend"
}

# 汇总
if ($script:Fails -gt 0) {
  Write-Output ("=== 验收结论：不通过（失败 {0} 项）===" -f $script:Fails)
  exit 1
}
Write-Output "=== 验收结论：全部通过 ==="

