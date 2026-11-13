---
name: petshop-booking-check
description: 宠物门店预约业务约束专项检查（题目02）。对数据库做跨环节一致性核查：状态枚举合法性、名额一致性、同宠物唯一预约、幂等键唯一、金额守恒、权限拒绝记录。触发场景：开发完成业务模块后自检、测试 Agent 设计用例前核对规则覆盖、验收前数据一致性核查。
---

# Skill：宠物预约业务约束检查

## 用途
对题目02 关键业务约束做专项检查——名额占用、预约时间、取消政策、服务进度、退款之间的跨环节一致性。

## 输入
- 题面规则（题目02 业务规则 1–8）
- 被检数据库（默认 `petshop_t02`，本机 3306 root/root）

## 执行步骤
1. 状态合法性：枚举 `bookings.status`，验证不存在题面未定义的迁移终态。
2. 名额一致性：每个时段 `有效预约数 ≤ 容量`，且 `slot_taken` 行数与有效预约数相等。
3. 宠物唯一性：同一 `(slot_id, pet_id)` 不存在多条未终态记录。
4. 幂等抽检：同一 `channel_txn` 不得对应两条 paid 支付；同一 `(booking_id, action)` 不得有多条 `service_records`。
5. 金额守恒：每个预约「支付成功合计 − 退款成功合计」必须等于 0 或 39600（单笔收款口径），异常即列出。
6. 取消政策：`cancel_review` 状态的预约不得存在核销记录；<2h 取消必须留有原因。
7. 权限抽检：越权/拒绝路径以测试报告为准，本 Skill 输出待人工核对的指针。

## 运行
```powershell
powershell -ExecutionPolicy Bypass -File skills/petshop-booking-check/scripts/check-business.ps1
```
可选参数：`-Db petshop_t02 -MysqlExe "C:\Program Files\MySQL\MySQL Server 8.0\bin\mysql.exe"`

## 输出格式
`检查项 | 结果(通过/失败) | 证据(数据)`，末尾汇总结论与需修复清单。
任一检查失败 → 输出缺陷条目（含复现数据），交回开发修复后**重新执行全部步骤**。

## 完成标准
全部检查通过或附不适用依据，无未处置失败项。
