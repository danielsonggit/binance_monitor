# R4-A1：轻量候选池执行台账

> 本文档从属于 [总体产品与开发 Roadmap](./v2-roadmap.md) 和
> [MHR-9 执行台账](./v2-signal-lifecycle-plan.md)。它只负责轻量候选发现、容量和延续策略，
> 不把候选包装成投资信号，也不授权 Telegram 通知或自动交易。

## 1. 文档状态

| 项目 | 内容 |
| --- | --- |
| 阶段 | R4-A1 / MHR-9-2（轻量候选池） |
| 状态 | IN_PROGRESS，schema 8 已部署，正在执行 24–48 小时影子验收 |
| 规则版本 | `candidate-rules-v1` |
| 特征版本 | `returns-v1` |
| 开发分支 | `feature/v2-market-radar` |
| 开始日期 | 2026-08-30 |
| 最后更新 | 2026-08-30 |
| 前置证据 | R3 已完成；R4-A0 七天分布分析已完成 |

## 2. 本阶段解决什么

现有 Top 5 只能列出已经涨得最多的标的。R4-A1 把高召回动量触发转换为一个稳定、受容量约束、
可重放的候选池，回答：

1. 哪些标的满足“可能正在启动”的最低发现条件；
2. 哪些标的因为质量、流动性、容量或冷却被排除；
3. 一个候选为什么进入、延续、被短暂保留或退出；
4. 下一阶段应该只为哪些标的采集 OI、funding 和 mark/premium。

候选资格只代表“值得继续收集证据”，不代表可以买入。R5 的确认状态和 R6 的结果评估尚未完成。

## 3. `candidate-rules-v1`

| 参数 | Crypto | TradFi |
| --- | ---: | ---: |
| 15m 触发 | `return >= max(板块 P95, +1.5%)` | `return >= max(板块 P95, +0.5%)` |
| 1h 触发 | `return >= max(板块 P90, +3.0%)` | `return >= max(板块 P90, +1.0%)` |
| 1h 最低成交额 | `$50,000` | `$15,000` |
| 24h 最低成交额 | `$1,000,000` | `$500,000` |
| 活动容量 | 20 | 10 |

共同规则：

- 方向固定为 `LONG`；当前不做空头候选。
- availability 必须为 `OPEN`；`LOW_ACTIVITY` 可以证明系统健康，但不能创建新候选。
- 15m 或 1h 任一有效周期触发即可发现；两个周期都无效时属于质量排除。
- 流动性门槛正式启用。最新连续七天 2016 个窗口中，它保留 Crypto 原始触发的 96.56%、
  TradFi 的 88.63%，候选数量 P50 仍为 11 和 3，不会把候选池砍空。
- 活动候选即使当前未命中，也连续保留两个五分钟窗口；第三次连续未命中时退出。
- 退出后进入 30 分钟冷却；冷却期即使再次触发也不能重新进入。
- 规则配置以 canonical JSON 和 SHA-256 不可变保存；同一规则版本不能被静默改写。

## 4. 容量和确定性优先级

已有 `ACTIVE` 候选优先保留，不与冷启动候选重新竞争。剩余槽位只在新的合格触发者之间分配，
排序键固定为：

1. 15m 和 1h 是否同时触发；
2. `max(return15m/threshold15m, return1h/threshold1h)` 降序；
3. 24h 成交额降序；
4. symbol 升序。

这个比值只用于容量截断，不是投资评分。被容量截断的标的写为 `REJECTED_CAPACITY`，不能伪装成
“没有触发”。

## 5. 状态与结果

候选头状态只有两个：

- `ACTIVE`：当前进入深度采集名单；
- `COOLDOWN`：已经退出，等待冷却到期。

每个 universe 标的、每个五分钟事件时点都保存一个 evaluation，结果只允许：

| 结果 | 含义 |
| --- | --- |
| `ENTERED` | 新进入或冷却结束后重新进入 |
| `CONTINUED` | 活动候选本窗口继续满足准入 |
| `MISS_HELD` | 本窗口未满足，但退出滞回尚未到三次 |
| `EXITED` | 连续第三次未满足，转入冷却 |
| `REJECTED_QUALITY` | availability 或有效周期不足 |
| `REJECTED_MOMENTUM` | 动量门槛未命中 |
| `REJECTED_LIQUIDITY` | 动量命中但流动性不足 |
| `REJECTED_CAPACITY` | 准入合格但板块容量已满 |
| `REJECTED_COOLDOWN` | 准入合格但仍处于冷却期 |

evaluation 同时保存收益、分位数、动态阈值、成交额、触发布尔值、容量排名、前状态、连续未命中、
冷却到期时间和稳定原因码。

## 6. 模块与数据模型

```text
internal/domain/signal/candidate.go          规则、候选状态、结果和 batch 约束
internal/candidatepool/calculator.go         纯事件时间计算器；不依赖 PostgreSQL
internal/candidatepool/service.go            输入/状态加载与持久化编排
internal/storage/postgres/
  candidate_pool_repository.go               事务、幂等、规则冲突和乱序门禁
  migrations/000008_candidate_pool.sql       additive schema 8
internal/v2/pipeline/returns.go               排名完成后运行候选计算
internal/v2/command/command.go                candidates --as-of 重放入口
internal/marketquery + internal/v2/api        只读候选 API
```

新增表：

- `candidate_rule_versions`：不可变规则 JSON、feature 版本和 SHA-256；
- `candidate_pool_members`：每个 instrument/direction/rule 的当前 `ACTIVE/COOLDOWN` 头；
- `candidate_evaluations`：按 `as_of` 分区的逐标的不可变评估事实；
- `collection_runs` 复用 `CANDIDATE_POOL_5M`，保存窗口汇总和幂等结果。

同一 `instrument + as_of + rule_version` 只有一条 evaluation；同一
`candidate-pool:<rule>:<as_of>` 只产生一条 run。写入使用 serializable transaction 和规则级
PostgreSQL advisory transaction lock。已经应用的时点直接返回原始结果，不依赖当前候选头重算；
未应用的乱序时点被拒绝，避免用未来状态污染过去。

候选身份使用不可变 `instrument_id`，不使用 symbol 作为状态主键。同名合约下线后重新上市会产生
新的 instrument 版本；旧候选按自身缺失证据退出，新合约独立评估，二者不会错误继承状态。

## 7. 命令与 API

手动生成当前时点或幂等重放已应用时点：

```bash
binance-monitor candidates \
  --as-of 2026-08-30T02:30:00Z \
  --env-file /absolute/path/.env.v2
```

查询活动候选：

```text
GET /api/v2/candidates
GET /api/v2/candidates?sector=CRYPTO&status=ACTIVE
GET /api/v2/candidates?sector=TRADFI&status=COOLDOWN
```

API 只读 PostgreSQL，不访问 Binance，不发送 Telegram。

## 8. 验收清单

- [x] 规则、状态、结果和 batch 约束位于独立领域模块。
- [x] 分位数、流动性、容量、已有候选优先、三窗口退出和冷却均有确定性单元测试。
- [x] migration 8 为 additive schema，不删除或改写 V1/V2 历史表。
- [x] PostgreSQL 保存不可变规则、逐标的 evaluation、候选头和 collection run。
- [x] 同时点重复执行返回原结果；未应用乱序时点被拒绝。
- [x] worker 流水线、`candidates --as-of` 和只读 API 已接入。
- [x] Go 全仓测试和 vet 通过。
- [x] jmk 隔离数据库执行全部 PostgreSQL 集成测试通过；临时库随后删除。
- [x] 正式库备份、migration 7 → 8、重复 migration 和回退文件已验证。
- [x] 线上完整五分钟窗口保存约 872 条 evaluation，API 与 run 汇总一致。
- [ ] 连续 24–48 小时统计容量截断、换手、退出、冷却和失败；V1/代理/reporter 不受影响。

代码完成不等于规则有效。影子验收只能证明系统行为稳定；候选是否有投资价值必须等 R6 的
T+收益、MFE 和 MAE。

## 9. 执行记录

### 2026-08-30 — 领域、持久化和接入代码完成

- R3 operational coverage 修正先完成并在 jmk 验收，随后才启动 R4-A1。
- 用连续七天生产事实重新计算流动性敏感性：Crypto 平均原始/流动性后候选
  `12.73/12.29`，TradFi `6.37/5.65`；正式启用文档中的冷启动流动性门槛。
- 新增纯计算器、schema 8、repository、pipeline、Cobra 命令和只读 API。
- 普通全仓测试与 vet 通过；jmk 隔离库中 15 组 PostgreSQL 测试全部通过，其中包括候选进入、
  幂等重放、未命中保留和 API 查询。
- 最终 Linux AMD64 PostgreSQL 测试二进制 SHA-256 为
  `410c6f3e4d631c4f1e4da5b58facfd2402503b96ef0afc67f83d319ef5d791e1`；同名合约重新上市的
  instrument_id 隔离也由纯计算器测试覆盖。
- 隔离数据库 `binance_radar_r4a1_test` 由本次测试创建，验证后已删除；正式数据库仍为 schema 7，
  本记录不授权自动迁移或常驻候选任务。

### 2026-08-30 — schema 8 正式部署，开始影子验收

- 部署前正式库使用 custom-format `pg_dump` 备份到
  `/home/daniel/services/binance-radar-v2/backups/binance_radar_pre_r4a1_20260830T1200CST.dump`；
  大小 676 MB，SHA-256 为
  `0a2c8148e3f85d2059b9879e6f2fafaf07e27bd961b7347a052cbbdc69198089`，并已使用容器内
  `pg_restore --list` 验证目录可读。
- 从提交 `c131807` 构建的 Linux AMD64 静态二进制 SHA-256 为
  `20d3e75cb5671888b1ced007295a39f47b8a57e63a3b01443cf6fce2896a7e06`；旧版回退文件保留为
  `/home/daniel/services/binance-radar-v2/binance-monitor-v2.pre-r4a1-20260830T1208CST`。
- migration 首次执行返回 `applied=1 current_version=8`，立即重复执行返回
  `applied=0 current_version=8`。
- 部署停机跨越 12:00 窗口，gap placeholder 没有 `state_symbols`，候选池按质量门禁拒绝该窗口，
  worker 暂时进入 `DEGRADED`。部署期间暂停 watchdog，避免将计划停机瞬态误报为生产事故；
  12:05 正常窗口完成后再启动 watchdog，首检直接为健康。
- 首个完整生产窗口 `2026-08-30 12:05 CST` 保存 872 条 evaluation：14 个 Crypto 候选
  `ENTERED`，621 个 `REJECTED_MOMENTUM`、236 个 `REJECTED_QUALITY`、1 个
  `REJECTED_LIQUIDITY`；ACTIVE 为 14，未触及 Crypto 20 的容量上限。
- `CANDIDATE_POOL_5M` collection run 为 `SUCCEEDED`，数据库与
  `/api/v2/candidates?sector=CRYPTO&status=ACTIVE` 均返回同一 as-of 和 14 个 ACTIVE。
- V2 worker/API/watchdog 均为 `active/running`、`NRestarts=0`；V1 继续运行，V2 reporter
  继续禁用，7890/7891 监听和代理边界未修改。R4-A1 进入 24–48 小时影子验收。

### 2026-08-30 — 非开放标的百分位热修复

- 12:10 自动窗口暴露 `candidate evaluation "MRNAUSDT" 无效`。候选事务在 batch 校验阶段整体
  回滚，12:10 没有部分 evaluation 或 member 写入，12:05 的 14 个 ACTIVE 头保持不变。
- 根因是百分位分布只包含 `OPEN` 标的，但旧计算器仍给 `LOW_ACTIVITY/MARKET_CLOSED` 等非开放
  标的评分；非开放异常值可能落在开放分布最大值之外，从而产生大于 100 的 percentile。
- 修复为非 `OPEN` 标的直接经过质量门禁，不计算 percentile 和 trigger；新增“非开放异常高收益”
  回归测试并通过全仓测试与 vet。修复提交为 `355eac1`。
- 热修复 Linux AMD64 静态二进制 SHA-256 为
  `2be2861fc741a2ef619819671bb874e9abc52ac2e6ac64d510d11554471d85a2`；部署前版本保留为
  `/home/daniel/services/binance-radar-v2/binance-monitor-v2.pre-r4a1-hotfix-20260830T1214CST`。
- 修复后重放 12:10 成功写入 872 条 evaluation：4 个 CONTINUED、10 个 MISS_HELD、4 个
  ENTERED，ACTIVE 为 18；再次重放返回 `already_applied=true`。
- 12:15 和 12:20 常驻 worker 自动窗口均成功。12:20 首次完整观察到 7 个 CONTINUED、6 个
  MISS_HELD、7 个 EXITED 和 1 个 ENTERED，证明三窗口退出、冷却写入与容量回补路径已在线运行。
- 12:20 operational coverage 为 `695/741 = 93.792173%`，worker 为 `HEALTHY`；watchdog 在该窗口
  后恢复，首检健康。V1 与三个 V2 服务均为 `active/running`、`NRestarts=0`。
- 截至 12:52，已连续保存 10 个候选窗口、8720 条 evaluation，`CANDIDATE_POOL_5M` 为
  `10/10 SUCCEEDED`；候选头为 13 个 ACTIVE、17 个 COOLDOWN，最新 operational coverage 为
  99.865047%，watchdog 持续健康。这是 24–48 小时影子验收的起始基线，不等同于最终验收完成。
