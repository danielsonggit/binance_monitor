# MHR-8：V2 独立健康监控与故障通知

> 本台账从属于 [总体产品与开发 Roadmap](./v2-roadmap.md)，不单独改变总体产品方向或阶段顺序。

## 状态

`COMPLETED`，开始日期 `2026-08-11`，完成日期 `2026-08-12`。

## 目标

MHR-7 已能把 worker、行情、快照、分析和回补质量写入 PostgreSQL/API，但没有无人值守的
主动告警。MHR-8 增加独立 `watchdog` 进程，市场报告 reporter 继续禁用。

watchdog 每分钟检查：

1. V2 API 是否可连接；
2. `/health/ready` 是否为 200，借此发现 PostgreSQL 不可用；
3. `/api/v2/quality` 中 worker heartbeat 是否健康且未过期；
4. WebSocket 最近事件、五分钟快照和多周期分析是否新鲜；
5. backfill 是否成功且没有未恢复缺口。

## 告警状态机

- 默认连续 3 次失败才建立 incident，避免一次网络抖动产生噪声；
- incident 建立后每个 Chat ID 最多发送一条故障通知；
- 默认连续 2 次恢复才关闭 incident，并发送一条恢复通知；
- 状态持久化到 jmk 本地文件，不依赖 PostgreSQL，因此数据库故障时仍能告警；
- Telegram 结果不确定时按“可能已发送”处理，避免自动制造重复消息；
- dry-run 是仓库安全默认值，部署验收期间只记录本应发送的内容，不调用 Telegram；
- jmk 初次启用可只读复用 V1 reporter 凭据；watchdog 专用 Bot/Chat/主题变量始终优先。

## 故障边界

watchdog 与 worker/API/数据库进程独立，因此可以发现它们停止或失效。但当前 Telegram 和
Binance 都通过 jmk 的 7890 出网：如果 7890 或整台 jmk 失联，Telegram 告警也无法立即发出。
watchdog 会保留 incident，并在通道恢复后发送恢复摘要；要覆盖整机/整条代理故障，仍需在
另一台机器部署外部监控，这是后续独立任务。

## 验收清单

- [x] 探测器覆盖 API 不可达、数据库 not-ready、heartbeat/行情/快照/分析过期和 backfill 缺口。
- [x] 状态机覆盖连续失败、连续恢复、去重、重启持久化和 Telegram 不确定结果。
- [x] 全仓测试、vet 和目标模块 race 通过。
- [x] jmk 安装独立 user-systemd unit，dry-run 下完成故障与恢复演练。
- [x] V1、V2 worker/API、7891 和正式 Telegram reporter 不受影响。
- [x] 使用现有 V1 Bot/Chat 发送明确的通道测试消息成功，随后通过独立 systemd drop-in 切换 live。

## 验收证据

- `binance-monitor watchdog` 独立于 PostgreSQL 进程运行，每 60 秒检查一次；默认连续 3 次失败
  建立 incident、连续 2 次健康关闭 incident。
- 状态使用原子替换写入 `/home/daniel/services/binance-radar-v2/state/watchdog.json`，权限
  `0600`；Telegram 凭据保存在独立 `watchdog.env`，权限同为 `0600`。
- jmk dry-run 演练中，前两次不可达只记录临时异常，第三次生成一条故障通知；第一次恢复等待，
  第二次恢复生成一条恢复通知，最终状态清零。
- Telegram 通道测试返回成功，接收人数量为 1；测试消息明确标注“监控已启用”，没有伪造故障。
- live unit 启动日志为 `dry_run=false`，当前 incident 为 false；V1、V2 worker、V2 API、watchdog
  均为 active，watchdog `NRestarts=0`，V2 市场报表 reporter unit 仍不存在。
- `go test ./...`、`go vet ./...`、watchdog/command/telegram race 与 systemd verify 全部通过。

## 七天确认方式

watchdog 负责观察过程中的主动故障/恢复通知，但不能替代最终完整性审计。影子运行达到 7 天时，
仍须从 PostgreSQL 证明完整时间网格至少包含 `7 × 24 × 12 = 2016` 个五分钟窗口、
`absent_windows=0`、`FAILED=0`，并复核最新 heartbeat、backfill 和服务状态。
