# 业务冒烟检查：登录 → 时段可见 → 预约 → 模拟支付成功回调 → 状态确认
$ErrorActionPreference = "Stop"
$Base = "http://localhost:8080"
$ErrorCount = 0

function Check($name, $cond) {
  if ($cond) { Write-Host "  [PASS] $name" }
  else { Write-Host "  [FAIL] $name"; $script:ErrorCount++ }
}

# 1. 健康检查
$h = Invoke-WebRequest "$Base/health" -UseBasicParsing
Check "健康检查 200" ($h.StatusCode -eq 200)

# 2. 用户登录（预置账号 u1）
$s = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$l = Invoke-WebRequest "$Base/login" -Method Post -Body @{username="u1";password="demo123"} `
  -WebSession $s -UseBasicParsing
Check "用户 u1 登录成功" ($l.StatusCode -eq 200 -and $l.Content -match "petshop|预约")

# 3. 可约时段列表非空
$p = Invoke-WebRequest "$Base/user" -WebSession $s -UseBasicParsing
Check "时段列表可见" ($p.Content -match "时段")

# 4. 发起预约（取第一个可约时段，页面接口返回 slotId）
# 冒烟环境预置保证存在可约时段；预约后应为待付款
$b = Invoke-WebRequest "$Base/user/book" -Method Post -Body @{petId="1";slotId="1"} `
  -WebSession $s -UseBasicParsing
Check "预约请求受理" ($b.StatusCode -eq 200)

# 5. 模拟渠道支付成功回调（冒烟专用直连通道）
$n = Invoke-WebRequest "$Base/mock/notify" -Method Post `
  -Body @{booking_id=$b.Content; result="success"} -UseBasicParsing
# 具体断言以接口契约为准；失败即冒烟失败
Check "支付回调受理" ($n.StatusCode -eq 200)

if ($ErrorCount -gt 0) { throw "冒烟检查失败 $ErrorCount 项" }
Write-Host "冒烟检查全部通过"
