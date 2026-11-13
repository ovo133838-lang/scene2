# 业务约束一致性核查（题目02）
param(
  [string]$Db = "petshop_t02",
  [string]$MysqlExe = "C:\Program Files\MySQL\MySQL Server 8.0\bin\mysql.exe"
)
$ErrorActionPreference = "Stop"
$env:MYSQL_PWD = "root"

function Sql([string]$q) {
  & $MysqlExe -uroot --host=127.0.0.1 --port=3306 -N -B -e $q $Db 2>$null
}

function Check($name, $cond, $detail) {
  if ($cond) { Write-Output ("[PASS] {0} | {1}" -f $name, $detail) }
  else { Write-Output ("[FAIL] {0} | {1}" -f $name, $detail); $script:Fails++ }
}
$script:Fails = 0

Write-Output "=== 宠物预约业务约束检查（库：$Db）==="

# 1. 状态枚举合法
$legal = "pending_payment","confirmed","cancel_review","cancelled","checked_in","in_service","finished","closed","payment_anomaly","no_show"
$bad = Sql "SELECT DISTINCT status FROM bookings" | Where-Object { $legal -notcontains $_ }
Check "状态枚举合法" (-not $bad) ("异常状态：" + ($bad -join ","))

# 2. 名额一致性：有效预约数 ≤ 容量
$over = Sql @"
SELECT t.id, t.capacity, COUNT(b.id) AS eff
FROM time_slots t LEFT JOIN bookings b ON b.slot_id=t.id AND b.active=1
GROUP BY t.id, t.capacity HAVING COUNT(b.id) > t.capacity
"@
Check "名额不超卖" (-not $over) ("超额时段：" + ($over -join "; "))

# 2b. slot_taken 行数 = 有效预约数
$mismatch = Sql @"
SELECT t.id, t.capacity,
  (SELECT COUNT(*) FROM bookings b WHERE b.slot_id=t.id AND b.active=1) AS eff,
  (SELECT COUNT(*) FROM slot_taken s WHERE s.slot_id=t.id) AS taken
FROM time_slots t
HAVING eff <> taken
"@
Check "slot_taken 与有效预约一致" (-not $mismatch) ("不一致时段：" + ($mismatch -join "; "))

# 3. 同宠物同时段唯一（未终态）
$terminal = "cancelled","closed","payment_anomaly","no_show"
$dup = Sql "SELECT slot_id, pet_id, COUNT(*) c FROM bookings WHERE active=1 GROUP BY slot_id, pet_id HAVING c>1"
Check "同宠物同时段唯一" (-not $dup) ("重复：" + ($dup -join "; "))

# 4a. channel_txn 唯一（paid）
$dupTxn = Sql "SELECT channel_txn, COUNT(*) c FROM payments WHERE status='paid' AND channel_txn IS NOT NULL AND channel_txn<>'' GROUP BY channel_txn HAVING c>1"
Check "支付交易号幂等" (-not $dupTxn) ("重复交易号：" + ($dupTxn -join "; "))

# 4b. (booking_id, action) 唯一
$dupAct = Sql "SELECT booking_id, action, COUNT(*) c FROM service_records GROUP BY booking_id, action HAVING c>1"
Check "服务记录幂等" (-not $dupAct) ("重复记录：" + ($dupAct -join "; "))

# 5. 金额守恒：收款-退款 ∈ {0} ∪ 实际收款金额集合
$amountErr = Sql @"
SELECT p.booking_id,
  COALESCE(SUM(CASE WHEN p.status='paid' THEN p.amount END),0) AS paid,
  COALESCE((SELECT SUM(r.amount) FROM refunds r WHERE r.booking_id=p.booking_id AND r.status='success'),0) AS refunded
FROM payments p GROUP BY p.booking_id
HAVING (paid - refunded) <> 0
   AND (paid - refunded) NOT IN (SELECT DISTINCT amount FROM payments WHERE status='paid')
"@
Check "金额守恒" (-not $amountErr) ("异常预约：" + ($amountErr -join "; "))

# 6. 取消政策：待审核预约不得已有核销
$il = Sql @"
SELECT b.id FROM bookings b
WHERE b.status='cancel_review'
  AND EXISTS (SELECT 1 FROM service_records s WHERE s.booking_id=b.id)
"@
Check "待审核期间无核销" (-not $il) ("违规：" + ($il -join ", "))

# 汇总
if ($script:Fails -gt 0) {
  Write-Output ("=== 结论：失败 {0} 项，需修复后全量重查 ===" -f $script:Fails)
  exit 1
}
Write-Output "=== 结论：全部通过 ==="

