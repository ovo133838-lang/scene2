# 业务冒烟检查：登录 → 时段可见 → 预约 → 支付 → 渠道成功回调 → 已确认
$ErrorActionPreference = "Stop"
$Base = "http://localhost:8080"
$ErrorCount = 0

function Check($name, $cond, $detail) {
  if ($cond) { Write-Host "  [PASS] $name" }
  else { Write-Host "  [FAIL] $name ($detail)"; $script:ErrorCount++ }
}

# 1. 健康检查
$h = Invoke-WebRequest "$Base/health" -UseBasicParsing
Check "健康检查 200" ($h.StatusCode -eq 200) ""

# 2. 用户登录（预置账号 u1）
$s = New-Object Microsoft.PowerShell.Commands.WebRequestSession
Invoke-WebRequest "$Base/login" -Method Post -Body @{username="u1";password="demo123"} `
  -WebSession $s -UseBasicParsing | Out-Null
$p = Invoke-WebRequest "$Base/user" -WebSession $s -UseBasicParsing
Check "用户 u1 登录并进入用户页" ($p.Content -match "可约时段") "user page"

# 3. 取一个可约时段并预约
$slots = [regex]::Matches($p.Content, 'name="slotId" value="(\d+)"')
Check "存在可约时段" ($slots.Count -gt 0) "slots"
if ($slots.Count -eq 0) { throw "无可用时段，无法冒烟" }
$sid = $slots[$slots.Count - 1].Groups[1].Value
Invoke-WebRequest "$Base/user/book" -Method Post -Body @{petId="1";slotId=$sid} `
  -WebSession $s -MaximumRedirection 0 -UseBasicParsing -ErrorAction SilentlyContinue | Out-Null
$p2 = Invoke-WebRequest "$Base/user" -WebSession $s -UseBasicParsing
Check "预约成功（待付款）" ($p2.Content -match "待付款") "book"

# 4. 发起支付并经模拟渠道成功回调
$bid = [regex]::Match($p2.Content, 'name="bookingId" value="(B\w+)"').Groups[1].Value
Check "取得预约单号" ($bid -ne "") $bid
Invoke-WebRequest "$Base/user/pay" -Method Post -Body @{bookingId=$bid} `
  -WebSession $s -MaximumRedirection 0 -UseBasicParsing -ErrorAction SilentlyContinue | Out-Null
$mock = Invoke-WebRequest "$Base/mock" -UseBasicParsing
$pid = ""
foreach ($row in ($mock.Content -split "<tr")) {
  if ($row -match [regex]::Escape($bid) -and $row -match 'value="(P\w+)"') { $pid = $Matches[1]; break }
}
Check "渠道生成支付单" ($pid -ne "") $pid
if ($pid -ne "") {
  Invoke-WebRequest "$Base/mock/pay" -Method Post -Body @{paymentId=$pid;action="success"} `
    -UseBasicParsing | Out-Null
  $p3 = Invoke-WebRequest "$Base/user" -WebSession $s -UseBasicParsing
  $row = ($p3.Content -split "<tr" | Where-Object { $_ -match [regex]::Escape($bid) }) -join ""
  Check "支付成功→已确认" ($row -match "已确认") $bid
}

if ($ErrorCount -gt 0) { throw "冒烟检查失败 $ErrorCount 项" }
Write-Host "冒烟检查全部通过"
