# MHR-9：候选信号与生命周期引擎执行台账

> 本文档是 MHR-9 的唯一执行台账。需求、设计决定、GitHub 参考项目、阶段状态、
> 验收证据和遗留风险均在此维护。任何阶段没有证据时不得标记为完成。
> 本台账从属于 [总体产品与开发 Roadmap](./v2-roadmap.md)，不单独改变总体产品方向或阶段顺序。

## 1. 文档信息

| 项目 | 内容 |
| --- | --- |
| 状态 | IN_PROGRESS |
| 当前阶段 | MHR-9-1 已完成 24 小时取证，正在修复 LOW_ACTIVITY 告警口径；MHR-9-2 尚未开始 |
| 所属版本 | V2 Market Radar |
| 开发分支 | `feature/v2-market-radar` |
| 创建日期 | 2026-08-20 |
| 最后更新 | 2026-08-30 |
| 前置阶段 | MHR-7 多周期榜单、MHR-8 独立 watchdog |

## 2. 背景与问题

V2 已连续采集 Binance USDⓈ-M 全市场行情，能够每 5 分钟计算 Crypto/TradFi 的
`15m`、`1h`、`4h`、`24h` 收益率与分板块排名。固定 7 天验收窗口内，行情、收益率和
排名三条流水线均为 `2016/2016`，时间缺口为 0，`FAILED` 为 0。

现有 Top 5 仍只能回答“哪些标的已经涨得最多”，不能可靠区分：

1. 刚进入加速阶段的机会；
2. 成交量与持仓量共同确认的趋势；
3. 主要由空头回补推动的上涨；
4. 资金费率过热、筹码拥挤的高风险状态；
5. 动量衰减、回撤扩大或证据反转；
6. TradFi 正常休市与真实数据源断流。

MHR-9 将榜单升级为可解释、可重放、有生命周期的信号系统，但仍不执行自动交易。

## 3. 阶段目标

1. 建立 Crypto 与 TradFi 分离、Binance 合约状态感知的数据质量语义。
2. 使用全市场轻量特征生成候选池，只对候选标的采集高成本深度数据。
3. 计算成交量、OI、资金费率、主动买卖、回撤和动量连续性特征。
4. 产生版本化、可解释的评分和证据，不把黑盒总分作为唯一判断。
5. 为每个标的和方向维护唯一活动信号生命周期。
6. 保证历史重放与实时计算使用同一套规则，固定输入得到固定输出。
7. 通过 PostgreSQL outbox 向测试 Chat ID 发送有意义的状态变化，正式群保持禁用。

## 4. 非目标

- 自动下单、仓位管理、止盈止损执行或交易账户接入；
- 用机器学习模型直接替代可解释冷启动规则；
- 在本阶段得出“规则已经盈利”或稳定胜率结论；
- 正式替换 V1 或停止 V1 定时报表；
- 建设完整 Web 控制台；
- 因引入参考项目而复制其完整交易引擎、数据库或交易所适配层；
- 将 AGPL/GPL 源码复制进本仓库，或未经许可证审查直接增加依赖。

## 5. 核心原则

1. **数据质量先于信号**：关键价格陈旧、缺口未恢复或市场状态不明时，禁止进入
   `CONFIRMED`。
2. **完成数据优先**：K 线类判断只使用已闭合 K 线，禁止使用未完成 K 线制造 repaint。
3. **事件时间驱动**：计算、状态转移和通知均携带 `event_time`，不能以进程执行时间替代。
4. **证据不可变**：状态事件保存当时的特征、规则版本和原因，后续调参不能改写历史解释。
5. **计算与投递分离**：信号事务与 outbox 同事务提交，Telegram 失败不能回滚信号历史。
6. **候选分层采集**：全市场低成本扫描，候选池高成本采集，避免 Binance 权重失控。
7. **无自动交易**：MHR-9 的最终输出是信号事件、API 和测试通知，不是订单。

## 6. 市场时段与数据质量

### 6.1 状态定义

| 状态 | 含义 | 是否计入数据缺失 | 是否允许新信号 |
| --- | --- | --- | --- |
| `OPEN` | 预期正常交易 | 是 | 是 |
| `MARKET_CLOSED` | 已知正常休市 | 否 | 否 |
| `LOW_ACTIVITY` | 开市但成交稀少 | 是，单独标记 | 仅允许 `WATCHING` |
| `DATA_MISSING` | 应有行情但没有 | 是 | 否 |
| `SOURCE_UNAVAILABLE` | Binance、代理或链路异常 | 是 | 否 |
| `UNKNOWN` | 无法证明市场状态 | 是 | 否 |

### 6.2 设计要求

- Binance USDⓈ-M Crypto 与 TradFi 永续合约在 `TRADING` 状态下均按 7×24 小时开放处理；
  传统标的休市只可能降低价格发现和活跃度，不能据此把 Binance 合约判定为休市。
- `exchangeInfo.status` 是首版动态可用性事实来源；当前 API 可见 `TRADING`、
  `PENDING_TRADING`、`SETTLING`，未来未知状态必须保守判定为 `UNKNOWN`。
- 首版不建立臆测的美股/商品交易日历；规则 provider 仍须版本化，以便 Binance 后续提供
  明确的合约 session 或维护窗口时扩展。
- 正常休市从实时覆盖率分母中排除，但原始未收到行情事实和判定依据必须保留。
- 质量 API 同时输出 raw coverage 与 session-adjusted coverage，禁止只暴露修正后的漂亮数字。
- 质量 API 还必须输出 operational coverage：分母与 session-adjusted 相同，分子为
  `OPEN + LOW_ACTIVITY`；它只回答行情源是否正常，不代表标的可用于确认信号。
- watchdog 的严重故障判断使用 operational coverage；`DATA_MISSING`、`SOURCE_UNAVAILABLE`
  和 `UNKNOWN` 降低该覆盖率，`LOW_ACTIVITY` 仍保留在 raw/session-adjusted 缺失审计中，
  但不再单独制造系统故障告警。
- 在无法从 Binance 元数据证明交易时段前，状态必须为 `UNKNOWN`，不能猜测为休市。

### 6.3 MHR-9-1 交付物与验收

本阶段必须交付：

1. 领域层市场状态、可用性规则、判定结果和判定原因模型；
2. 可替换的 availability rule provider，规则来源、版本和证据可审计；
3. 按 `instrument + event_time` 判定状态的确定性 classifier；
4. 同时计算 raw expected/actual/coverage 与 session-adjusted expected/actual/coverage；
5. collection run、quality API、worker heartbeat 和 watchdog 对新质量语义的适配；
6. 历史时点重放入口和 PostgreSQL repository/migration 集成测试。

完成 MHR-9-1 必须逐项证明：

- 原始快照、原始 expected/actual 和既有审计记录不被修改或删除；
- raw coverage 与 session-adjusted coverage 在同一质量结果中同时可查；
- TradFi 不能因为其传统标的休市而被判定为 `MARKET_CLOSED`；单标的 ticker 陈旧应进入
  `LOW_ACTIVITY` 并继续保留在 raw/session-adjusted 缺失审计中；
- TradFi 或 Crypto 处于 `TRADING/OPEN` 但完全没有行情时仍计为 `DATA_MISSING` 并可触发故障；
- Crypto 和 TradFi 均不得仅因低活跃被推断为休市，除非存在 Binance 官方合约状态、维护或
  下线证据；
- `UNKNOWN`、`DATA_MISSING`、`SOURCE_UNAVAILABLE` 均禁止新建候选和进入 `CONFIRMED`；
- Binance WebSocket、7890 或整体行情源断流时，session 规则不能把系统性缺失排除掉；
- 同一规则版本、instrument 和 event time 的历史重放必须得到相同市场状态与覆盖率；
- V1、现有多周期收益率/排名结果、7891 和历史 PostgreSQL 数据保持兼容；
- 单元测试、PostgreSQL 集成测试、`go test ./...`、`go vet ./...` 和目标包 race 通过。

MHR-9-1 明确不采集 OI、funding、mark price 或 taker 数据，不实现评分、信号生命周期和
Telegram 投资信号；这些分别属于后续 MHR-9-2 至 MHR-9-7。

## 7. 候选池

### 7.1 冷启动进入条件

满足下列任一动量条件，并通过数据质量和最低流动性门槛后进入 `WATCHING`：

- `15m` 收益位于板块 P95 以上，且 Crypto 至少 `+1.5%`、TradFi 至少 `+0.5%`；
- `1h` 收益位于板块 P90 以上，且 Crypto 至少 `+3.0%`、TradFi 至少 `+1.0%`；
- 已有活动生命周期需要继续跟踪，即使暂时跌出候选排名。

以上阈值是 `candidate-rules-v1` 冷启动配置，不是已证明有效的投资规律。

### 7.2 候选池约束

- 按板块分别限制候选数量，避免 Crypto 数量压倒 TradFi。
- 已有活动生命周期拥有跟踪优先级，不能因瞬时跌出 Top N 立即停止采集。
- 候选进入、延续、退出均写审计事件和规则版本。
- 候选池变化必须幂等，相同 `symbol + direction + as_of + rule_version` 只能产生一次结果。

## 8. 深度特征

| 类别 | 特征 | 建议数据源 | 默认频率 |
| --- | --- | --- | --- |
| 量能 | 15m 成交量 / 过去 20 根中位数 | Binance Futures Kline | 5 分钟 |
| 持仓 | OI 当前值、15m/1h 变化 | Open Interest/History | 5 分钟 |
| 拥挤 | 当前 funding、历史分位数 | Funding Rate | 5–15 分钟 |
| 基差 | mark price 与合约价格偏差 | Mark Price | 5 分钟 |
| 主动性 | taker buy/sell ratio | Taker Buy/Sell Volume | 5 分钟 |
| 连续性 | 动量加速度、连续正收益窗口 | 本地快照/K线 | 5 分钟 |
| 风险 | 距短周期高点回撤、波动扩张 | 本地快照/K线 | 5 分钟 |

采集器必须具有共享权重预算、端点级限速、有限重试、数据新鲜度、幂等写入和缺口审计。
端点不可用时降级特征质量，不得用零值伪装真实数据。

## 9. 可解释评分

冷启动总分为 0–100，默认组成如下：

| 维度 | 权重 |
| --- | ---: |
| 动量与板块相对强度 | 30% |
| 成交量确认 | 20% |
| OI 结构 | 20% |
| 资金费率与拥挤惩罚 | 15% |
| 流动性与连续性 | 10% |
| 数据质量 | 5% |

要求：

- 保存每个子分数、原始输入、归一化方法和规则版本；
- 正面证据和风险证据分别保存，不能只保存一段最终描述；
- 缺失深度特征不能按 0 分简单处理，必须区分“中性证据”和“未知证据”；
- 关键特征未知时降低可达到的最高状态，而不是只降低总分；
- 所有百分比与价格继续使用 `decimal.Decimal` 存储；技术指标库若使用 `float64`，必须在
  独立适配层完成有界转换并测试 NaN、Inf、warmup 和舍入边界。

## 10. 信号生命周期

### 10.1 状态

```text
NORMAL
  -> WATCHING
  -> STARTING
  -> CONFIRMED
  -> CROWDED / WEAKENING
  -> INVALIDATED
  -> CLOSED
```

### 10.2 约束

- 每个 `instrument + direction` 最多一个活动生命周期。
- `WATCHING` 默认不通知；其余是否通知由版本化 transition rule 决定。
- `CONFIRMED` 必须同时满足数据质量、动量和至少一项成交量/OI确认。
- `CROWDED` 是风险状态，不等价于立即看跌；必须保存触发的 funding、涨速或 OI 风险证据。
- `WEAKENING` 可以恢复到 `CONFIRMED`，也可以进入 `INVALIDATED`。
- 状态转移使用数据库事务和乐观并发/唯一约束，进程重启与任务重放不能重复产生事件。
- 每个事件保存 before/after、规则版本、特征快照引用、证据、event time 和计算时间。

## 11. 数据模型草案

最终 migration 前需通过 repository 集成测试确定字段，当前计划包含：

- `instruments.exchange_status`：Binance 原始合约状态及其有效期版本；
- `candidate_feature_snapshots`：候选深度特征、质量和数据时间；
- `signal_rule_versions`：不可变规则配置、checksum 和启停时间；
- `signal_lifecycles`：当前生命周期头与唯一活动约束；
- `signal_events`：不可变状态转移和证据；
- 复用 `notification_outbox` 与 `notification_deliveries`；
- 扩展 `collection_runs` 与 `system_heartbeats`，不另造重复任务审计表。

所有表必须有明确保留策略、索引、幂等键和向前 migration 测试。

## 12. 模块边界

```text
internal/domain/market        市场状态、质量语义与可替换 availability rule
internal/domain/signal        候选、评分、状态与事件
internal/depthdata            OI/funding/mark/taker 端口
internal/binance              Binance 深度数据适配器
internal/feature              深度特征计算
internal/signalengine         版本化规则与状态转移编排
internal/replay               历史事件时间重放
internal/storage/postgres     migration 与 repository
internal/v2/api               只读候选/信号查询
internal/v2/reporter          测试通知与 outbox dispatcher
```

领域层不能依赖 Binance、pgx、Telegram 或具体技术指标库。第三方指标只能位于适配层后面。

## 13. GitHub 参考项目评估

调研日期：2026-08-20。以下结论基于项目 README、许可证和核心策略接口；只借鉴设计，除非
后续依赖评审明确通过，否则不复制代码、不直接引入完整框架。

| 项目 | 许可证 | 值得参考 | 不采用/限制 |
| --- | --- | --- | --- |
| [Freqtrade](https://github.com/freqtrade/freqtrade) | GPL-3.0 | 闭盘生成信号、entry/exit 冲突处理、dry-run、backtest、lookahead/repaint 防护 | 不复制 GPL 代码；不引入 Python 运行时或自动下单 |
| [QuantConnect LEAN](https://github.com/QuantConnect/Lean) | Apache-2.0 | `AlphaModel -> Insight` 分层、Insight 的方向/周期/置信度语义、组合模型 | C#/Python 完整引擎过重；只借鉴领域模型与接口 |
| [NautilusTrader](https://github.com/nautechsystems/nautilus_trader) | LGPL-3.0 | 确定性事件驱动、同一策略用于 replay/live、handler 生命周期、状态持久化 | 不引入 Rust/Python 引擎，不复制 LGPL 实现 |
| [Ninjabot](https://github.com/rodrigo-brito/ninjabot) | MIT | Go `Strategy` 接口、warmup、闭盘 `OnCandle`、回测和实时共用策略 | 框架面向订单执行；本项目不导入 broker/order 层 |
| [go-talib](https://github.com/markcheno/go-talib) | MIT | 纯 Go TA-Lib 指标、测试对照参考 TA-Lib，适合作为指标适配层候选 | `[]float64` 与前置零值需要严格包装；引入前做精度和维护性 spike |
| [Indicator Go v2](https://github.com/cinar/indicator) | AGPL-3.0/商业许可 | 流式 indicator、组合策略、context cancellation、outcome 计算 | 不引入 v2、不复制实现；AGPL 与当前依赖策略不符。v1 虽为 MIT 但接口老旧 |
| [VectorBT](https://github.com/polakowo/vectorbt) | Apache-2.0 + Commons Clause | 大规模参数扫描、walk-forward、信号分布和绩效分析 | 仅作为离线研究思路；许可证和 Python 栈使其不适合作为生产依赖 |
| [GoCryptoTrader](https://github.com/thrasher-corp/gocryptotrader) | MIT | 事件式 backtester、数据与执行分离、交易所故障处理 | 完整框架过大且官方明确仍在开发；不作为依赖 |

### 13.1 采用结论

1. 领域边界采用 LEAN 的“数据输入 -> 可解释 Insight/Signal”思想，但输出为本项目的
   `SignalProposal`，不直接对应订单。
2. 实时和重放采用 NautilusTrader 的确定性事件时间原则，同一规则函数不感知运行模式。
3. 信号只基于闭合 K 线，冲突信号显式拒绝，采用 Freqtrade 的正确性原则。
4. Go 策略接口参考 Ninjabot 的 warmup/闭盘回调，但不允许领域接口接收 broker。
5. `go-talib` 是唯一进入依赖 spike 的指标候选；不在台账阶段直接修改 `go.mod`。
6. MHR-10 可评估独立 Python 研究容器，但 PostgreSQL 事实库和 Go 生产规则仍是唯一生产口径。

## 14. 分阶段执行计划

状态只允许 `PENDING`、`IN_PROGRESS`、`CODE_COMPLETE`、`BLOCKED`、`COMPLETED`。

| ID | 阶段 | 状态 | 完成标准 | 预计工作量 |
| --- | --- | --- | --- | --- |
| MHR-9-0 | 台账与开源项目评估 | COMPLETED | 文档进入索引；范围、参考模式和许可证决定明确 | 0.5 天 |
| MHR-9-1 | 市场时段与质量语义 | IN_PROGRESS | migration 7 已部署且首个窗口通过；仍需 24 小时连续验收 | 1–1.5 天 |
| MHR-9-2A0 | 候选指标分布基线 | COMPLETED | 七天只读分布、候选数量、换手和 K 线派生指标在 jmk 实测 | 0.5 天 |
| MHR-9-2 | 候选池与深度数据采集 | PENDING | 候选幂等；OI/funding/mark/taker 限速采集和审计通过 | 2–3 天 |
| MHR-9-3 | 深度特征与可解释评分 | PENDING | 子分数、证据、质量上限和规则版本可重放 | 1–2 天 |
| MHR-9-4 | 生命周期与 PostgreSQL 持久化 | PENDING | 唯一活动生命周期；并发/重启/重复执行安全 | 1.5–2 天 |
| MHR-9-5 | Replay 与只读 API | PENDING | 固定输入结果确定；活动/历史/详情 API 可查 | 1–1.5 天 |
| MHR-9-6 | 结果评估与影子样本 | PENDING | T+收益、MFE、MAE 到期计算幂等；规则效果按版本可查 | 1–2 天开发 + 样本积累 |
| MHR-9-7 | Telegram dry-run 与测试群 | PENDING | 状态通知无重复；正式 Chat ID 保持禁用 | 0.5–1 天 |
| MHR-9-8 | jmk 影子验收 | PENDING | 连续 24–48 小时；数据、状态、通知和 V1 隔离达标 | 1–2 天观察期 |

预计开发工作量为 7–11 个开发日，另加 24–48 小时影子观察。估时不是完成证据。

## 15. 验收标准

- 正常休市不产生数据源中断告警，raw coverage 仍可审计；
- 数据缺失、价格陈旧或关键证据未知时不能进入 `CONFIRMED`；
- 所有状态事件带规则版本、event time、特征引用和结构化证据；
- 同一输入、同一规则版本重放得到相同候选、评分和状态转移；
- 每个 instrument/direction 最多一个活动生命周期；
- worker 重启、数据库重连和重复调度不产生重复事件或通知；
- 深度端点遵守共享权重预算，429 和暂时失败有有限重试与明确降级；
- 测试 Telegram 24–48 小时业务重复为 0，通知数量在配置上限内；
- V1、7891、现有 PostgreSQL 数据和 V2 多周期榜单行为不变；
- `go test ./...`、`go vet ./...`、核心 race 与 PostgreSQL 集成测试通过。

## 16. 主要风险与处理

| 风险 | 后果 | 处理 |
| --- | --- | --- |
| 把传统标的交易时段套给 Binance TradFi 永续 | 隐藏 24/7 合约的真实缺数 | `TRADING` 合约按 24/7；仅接受 Binance 官方状态/维护证据 |
| 候选数量突增 | REST 权重超限 | 分板块上限、优先级队列、共享限速和降级 |
| OI/funding 历史不足 | 冷启动分位数失真 | 保存覆盖起点；未达 warmup 时标记未知，不填零 |
| 多个弱指标凑出高总分 | 产生虚假确认 | 状态转移除总分外设置必要证据门槛 |
| lookahead/repaint | 回放优异但实时失效 | 只使用闭合 K 线；按 event time 查询；加入未来数据泄漏测试 |
| 规则修改破坏历史解释 | 无法复盘 | 规则版本不可变；新参数发布新版本 |
| 开源许可证污染 | 约束项目分发 | 依赖准入检查；GPL/AGPL 项目只参考思想，不复制代码 |
| 测试通知过多 | Telegram 噪声 | 状态变化触发、冷却、每日上限、测试 Chat ID |

## 17. 用户待确认参数

这些参数不阻塞 MHR-9-1，但必须在 MHR-9-7 前确认：

1. 测试 Chat ID 是否与正式群分开；默认：分开。
2. 每日投资信号通知上限；默认：10 条。
3. Binance TradFi 合约为非 `TRADING` 状态时是否禁止产生新信号；默认：禁止，但继续跟踪已有生命周期。
4. 是否允许未来增加独立 Python 离线研究容器；默认：MHR-9 不增加。

## 18. 文档更新规则

1. 开始一个阶段时将其标记为 `IN_PROGRESS`，同一时间最多一个阶段处于该状态。
2. 完成阶段时在同一变更中记录代码路径、测试命令、结果和关键指标。
3. 没有实际证据的验收项不得勾选或标记为 `COMPLETED`。
4. 新风险和需求变化先更新本文档，再修改实现。
5. 新增第三方库必须记录版本、许可证、使用边界和不用标准库/现有库的原因。
6. jmk 部署、Telegram 启用和正式 Chat ID 变更必须独立记录，不能因代码完成自动执行。

## 19. 执行记录

### 2026-08-20 — MHR-9-0 完成

- 建立 MHR-9 唯一执行台账并加入 docs 索引。
- 固定市场时段、候选深度采集、可解释评分、生命周期、replay 和测试通知边界。
- 评估 Freqtrade、LEAN、NautilusTrader、Ninjabot、go-talib、Indicator Go、VectorBT 和
  GoCryptoTrader 的核心接口与许可证。
- 决定不引入完整交易机器人框架；GPL/AGPL 项目只借鉴设计，不复制代码。
- 将 `go-talib` 记录为 MHR-9-3 前的候选依赖 spike，本阶段未修改 `go.mod`。
- 下一步为 MHR-9-1：实现市场时段感知与 raw/session-adjusted 双覆盖率。

### 2026-08-20 — MHR-9-1 开始

- 将 MHR-9-1 标记为 `IN_PROGRESS`。
- 先审计现有 snapshot collection run、quality API、worker heartbeat、watchdog 和 PostgreSQL
  migration/repository 边界，再实现领域模型，避免把交易时段判断耦合进 Binance 或 HTTP 层。

### 2026-08-22 — MHR-9-1 代码完成

- 新增 `binance-usdm-availability-v1` 规则和可替换 `AvailabilityRule` 端口，状态包括
  `OPEN`、`MARKET_CLOSED`、`LOW_ACTIVITY`、`DATA_MISSING`、`SOURCE_UNAVAILABLE`、`UNKNOWN`。
- 经 Binance 官方资料与实时 `exchangeInfo` 核对，确认 USDⓈ-M TradFi 永续在 Binance
  `TRADING` 状态下同样是 7×24 小时合约；没有引入虚假的传统交易所 session 日历。
- V2 universe 改为保存完整的受支持合约目录，不再静默丢弃 `PENDING_TRADING`、`SETTLING`
  或未来未知状态；migration `000007` 增加 `exchange_status`，状态变化按有效期生成新版本。
- snapshot collection run 同时保存 raw 和 session-adjusted 的 expected、actual、missing、
  coverage percent、状态计数、逐状态 symbol 和规则版本；旧的顶层 expected/actual/missing
  保持 raw 口径，未删除或改写历史记录。
- 收益率、K 线回补和排名仅选择事件时点为 `TRADING` 的合约；状态不明不会进入后续信号输入。
- worker heartbeat 写入 `snapshot_coverage`，collector 首版健康判断使用 adjusted coverage；整体
  WebSocket 断流时所有应评估标的进入 `SOURCE_UNAVAILABLE`，因此 watchdog 不会被休市规则掩盖。
- `/api/v2/quality` 返回最新 snapshot quality；新增
  `/api/v2/quality/snapshots?as_of=<RFC3339>`，可按对齐的 5 分钟事件时点读取历史判定与审计证据。
- 验证证据：
  - `go test -count=1 ./...`：全部通过；
  - `go vet ./...`：通过；
  - 核心包 `go test -race -count=1`：全部通过；
  - jmk 临时隔离库 `binance_radar_mhr9_test`：13 组 PostgreSQL migration/repository 集成测试全部通过；
  - 测试后已删除临时数据库和测试二进制，未迁移、重启或修改线上 V2/V1 服务。
- 当前唯一下一步为完成部署后的 24 小时质量观察；通过后才进入 MHR-9-2，
  实现候选池、候选延续策略，以及 OI/funding/mark/taker 的分层限速采集。

### 2026-08-28 — MHR-9-1 部署完成，进入观察

- 部署前确认 V1、V2 worker/API/watchdog 与 PostgreSQL 正常，正式库约 6.7GB、schema 6、
  736 个 `TRADING` 合约，最新多周期指标有效率为 100%。
- 创建 PostgreSQL custom-format 备份
  `/home/daniel/services/binance-radar-v2/backups/binance_radar_pre_mhr9_1_20260828T0317Z.dump`，
  大小 738,496,715 bytes；`pg_restore --list` 和 SHA-256 校验通过，checksum 为
  `256b82d4d5c6cd300c27b5e4acf4c55d0d72840f9fdcbdf323789795a1d1e0b4`。
- 构建并部署静态 Linux AMD64 二进制，SHA-256 为
  `11cb8c771b5f5b1ceca63f546d41569b924f4e4fff7c9b5ad0dbcc03a85e8141`；旧二进制保留为
  `/home/daniel/services/binance-radar-v2/binance-monitor-v2.pre-mhr9-1-20260828`。
- 维护窗口先停止 watchdog，再停止 V2 worker/API；V1 全程保持 active、未重启，7890/7891
  监听和用途不变。
- migration 6 → 7 为 `applied=1`，重复执行为 `applied=0`；正式目录同步得到
  `TRADING=736`、`SETTLING=130`、`PENDING_TRADING=1`。
- 首个完整窗口 `2026-08-28 11:40 CST`：raw `731/867 = 84.313725%`，adjusted
  `731/736 = 99.320652%`；`OPEN=731`、`MARKET_CLOSED=131`、`LOW_ACTIVITY=4`、
  `DATA_MISSING=1`、`SOURCE_UNAVAILABLE=0`、`UNKNOWN=0`。
- worker heartbeat 已恢复 `HEALTHY`；API readiness、历史质量查询、多周期收益与榜单通过；
  watchdog 在 worker 健康后恢复运行，前两次轮询均为健康且部署后无 error 日志；V2 reporter 仍禁用。
- 首个新规则分析窗口保存 736 行 feature、8 组 ranking 和 40 个榜单项；worker/API/watchdog
  `NRestarts=0`，V1 自 2026-08-01 启动后未因本次部署重启。
- 停机跨过 `11:35` 边界，系统按设计登记 1 个不可精确回补的维护窗口缺口；不删除或伪造该记录。
- MHR-9-1 保持 `IN_PROGRESS`，从首个完整窗口开始观察至少 24 小时后才能标记完成。

### 2026-08-28 — R4-A0 候选指标分布分析完成

- 用户明确同意在 R3 观察期间并行执行只读准备工作；没有修改 schema、线上二进制、systemd、
  V1、7890/7891 或 Telegram 状态。
- 新增 `candidate-analysis` Cobra 命令和独立 `internal/candidateanalysis` 研究模块；支持最新或
  指定结束时点、1 小时至 14 天 lookback、Markdown/JSON 输出。
- 分析只读取有效 `returns-v1` 与闭合 15 分钟 K 线；加速度、连续收盘上涨、前 20 根成交额
  中位数和一小时高点回撤均有 event-time 连续性门禁。
- jmk 七天实测范围为 2026-08-21 15:35 至 2026-08-28 15:35（北京时间），覆盖 2,017 个
  五分钟窗口、1,409,349 行有效 feature 和 494,107 根闭合 K 线。
- 当前收益门槛下，Crypto 候选数 P50/P90/最大为 `13/33/75`，TradFi 为 `5/17/26`；
  五分钟原始换手率 P50 分别为 `53.85%/60.00%`，证明 R4-A1 必须加入容量和退出滞回。
- 冷启动约束、详细分布、局限和下一步记录在
  [R4-A0 候选指标分布分析](./v2-candidate-distribution-analysis.md)。
- R3 仍保持 `IN_PROGRESS`；R4-A0 完成不授权启动持久化候选任务，必须先完成 R3 24 小时验收。

### 2026-08-30 — MHR-9-1 24 小时取证完成，发现告警口径缺陷

- 从首个完整窗口起审计 558 个五分钟窗口：`MARKET_SNAPSHOT_5M`、`RETURN_FEATURES_5M`、
  `RANKINGS_5M` 时间网格持续运行，worker/API/watchdog 均为 `NRestarts=0`；最新四周期特征
  `2964/2964` 有效，回补缺口为 0。
- 558 个 snapshot 窗口的 `DATA_MISSING` 最大值为 1（仅部署首窗），其后始终为 0；周日最新
  `LOW_ACTIVITY=105`，其中 TradFi 104、Crypto 1。这是可审计的低成交事实，不是漏采或休市推断。
- `2026-08-29 22:34:58 CST` WebSocket 因超过新鲜度窗口主动重连；`22:35` 窗口将 872 个标的
  全部分为 `SOURCE_UNAVAILABLE`，adjusted coverage 为 0。watchdog 连续 3 次失败后建立 incident，
  数据源在约 5 秒后重连，下一窗口恢复，watchdog 连续 2 次健康后关闭 incident。
- 取证同时证明首版告警口径存在缺陷：周末 TradFi 低活跃会把 adjusted coverage 压到 85% 以下，
  造成大量短暂故障/恢复通知。不能通过删除 `LOW_ACTIVITY`、伪造 `MARKET_CLOSED` 或任意下调
  阈值掩盖该问题。
- 修正决定：raw 和 session-adjusted 口径、状态计数、逐标的证据及历史记录全部保留；新增
  operational coverage，使用 `OPEN + LOW_ACTIVITY` 衡量行情源运行健康。候选/信号仍只允许
  `OPEN`，因此该变更不会让低活跃标的进入 `CONFIRMED`。
- MHR-9-1 在修正版通过单元/集成测试、部署后周末实测和一次故障语义验证前继续保持
  `IN_PROGRESS`；R4-A1 不提前启动。
