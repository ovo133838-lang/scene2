---
name: petshop-acceptance-check
description: 题目02 最终验收检查。依据原始题面对运行中的系统逐项验收：健康检查、登录、核心业务流（预约→支付→确认）、业务约束一致性、质量证据（测试/覆盖率/竞态）。触发场景：测试通过、申请合并 main 之前的验收 Agent 独立会话。
---

# Skill：题目02 验收检查

## 用途
依据原始题面对最终版本逐项验收，产出可核查的验收结论。

## 输入
- 原始题面、被验收 commit 哈希
- 运行中的服务（默认 http://localhost:8080）
- 测试报告与覆盖率原始输出

## 执行步骤
1. 读取原始题面，列出逐项验收清单（规则 1–8 + 演示场景 + 共同要求）。
2. 实际运行验收探针：健康、登录、可约时段、预约、支付回调、状态确认（`scripts/acceptance-smoke.ps1`）。
3. 数据一致性：调用 `petshop-booking-check` Skill 的业务约束核查。
4. 质量证据核查：`go vet`、测试与覆盖率原始输出（整体与分包 ≥85%）、与被验收 commit 对应。
5. 假设核对：实现取舍对应 `docs/01-需求分析.md` 假设清单。

## 运行
```powershell
powershell -ExecutionPolicy Bypass -File skills/petshop-acceptance-check/scripts/acceptance-smoke.ps1
```
可选参数：`-Base http://localhost:8080`

## 输出格式
逐项 `编号 | 要求摘要 | 结论(通过/不通过) | 证据位置`，末尾总结论与遗留风险。
任何一项不通过 → 整体验收不通过，缺陷退回开发；修复后重跑全部步骤。

## 完成标准
全部清单项通过且证据可追溯，结论与被验收 commit 哈希绑定。
