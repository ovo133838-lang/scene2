# scene2 — 题目02：宠物门店的预约、接待、服务与取消退款

Go (go-zero 脚手架) + MySQL 8 + Redis 实现的宠物门店预约经营系统：四角色页面（用户/前台/技师/客服/店长）、模拟支付渠道、预约全生命周期（占位→支付→核销→服务→完成）、取消审批与退款、异常支付处理、售后服务（投诉→客服批准退款/驳回）。

> 题面：`D:/Desktop/Go项目业务场景题.md` 题目02。业务代码位于 `apps/petshop/`，开发在 `dev` 分支，经 PR 合入 `main`（main 已开启分支保护）。

## 环境要求

- Go ≥ 1.25；MySQL 8（127.0.0.1:3306，root/root）；Redis（127.0.0.1:6379）
- Windows 本机无 cgo：竞态检测需 WSL/ Linux（见下）

## 一键运行

```powershell
# 构建+建库+种子数据+启动（自动执行，监听 :8080）
cd apps/petshop
go build -o petshop-server.exe ./cmd/server
./petshop-server.exe
# 打开 http://localhost:8080  预置账号：manager / reception / staff / service / u1~u3，口令 demo123
```

## 测试与质量门禁

```powershell
cd apps/petshop
# 单测 + 集成（E2E 每轮独立建库，自动清理）
go test ./internal/... ./e2e/ -count=1
# 合并覆盖率（门禁 ≥85%，当前 87.6%，分包全部 ≥85%）
go test ./internal/... ./e2e/ -count=1 `
  -coverpkg=./internal/... -coverprofile cover.out
go tool cover -func cover.out

# 竞态检测（Linux/WSL）
CGO_ENABLED=1 go test -race ./internal/... ./e2e/ -count=1
```

## 六阶段流水线（质量→门禁→构建→部署→健康→冒烟）

```powershell
powershell -ExecutionPolicy Bypass -File deploy/pipeline.ps1
```

最近一次执行：全部通过，版本 43a2ecc，冒烟 7 项 PASS（`04-部署/pipeline-run.log`）。

## Skill（可执行）

```powershell
# 业务约束一致性检查（8 组 SQL 核查）
powershell -ExecutionPolicy Bypass -File skills/petshop-booking-check/scripts/check-business.ps1
# 验收探针（健康/登录/核心流/一致性/幂等）
powershell -ExecutionPolicy Bypass -File skills/petshop-acceptance-check/scripts/acceptance-smoke.ps1
```

## 目录

```
apps/petshop/     业务源码 + E2E（e2e/）
skills/           两份自编 Skill（SKILL.md + scripts）
deploy/           pipeline.ps1 / smoke.ps1
docs/…            需求分析、任务清单、技术设计等过程文档
```

依赖版本见 `apps/petshop/go.mod`（go-zero、go-sql-driver/mysql、go-redis）。
