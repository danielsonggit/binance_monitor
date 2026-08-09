# Binance Market Radar V2：开发进度

更新时间：2026-08-09

当前分支：`feature/v2-market-radar`

## 当前阶段

- Phase 0：产品与架构设计已完成；
- Phase 1：数据库与采集开发项完成，连续影子验收留到 MHR-7；
- Phase 2：多周期收益率与质量门禁已完成，候选排名和生命周期继续开发；
- Phase 3 及以后：尚未开始。

## 已完成的 Phase 1 能力

1. CLI 已统一使用 Cobra；V1 默认 CLI 和部署行为保持兼容，显式 `binance-monitor v1` 也可进入 V1。
2. Cobra 命令树注册了 `migrate`、`worker`、`api`、`backfill`、`features` 五个独立 V2 角色。
3. V2 配置位于 `internal/v2/config`，不把数据库和 Web 配置混入 V1。
4. PostgreSQL 访问位于 `internal/storage/postgres`，使用 pgx/v5 连接池。
5. migration 使用 Go embed 打入二进制，具有 advisory lock、事务、版本和 checksum 防篡改。
6. 第一版 schema 包含：
   - 合约有效期 `instruments`；
   - 分区表 `market_snapshots_5m`；
   - 分区表 `klines_15m`；
   - 分区表 `return_feature_snapshots`；
   - 采集审计 `collection_runs`；
   - 组件心跳 `system_heartbeats`。
7. V2 worker 已形成独立生命周期，能周期写入数据库心跳。
8. V2 API 已提供：
   - `GET /health/live`；
   - `GET /health/ready`，实际检查 PostgreSQL。
9. `compose.v2.yaml` 与 V1 `compose.yaml` 完全分离，使用独立 project、网络和数据卷。
10. 已通过真实 Docker/PostgreSQL 验证：首次 migration 应用 1 个版本，第二次应用 0 个版本；worker 心跳为 `HEALTHY`；API live/ready 均为 200。
11. 已完成 Binance 合约目录链路：
    - `exchangeInfo` 独立读取，不为目录同步额外请求 24h ticker；
    - 独立 `market.Instrument` 领域模型；
    - Crypto、TradFi 和 USDT/USDC 白名单映射；
    - 每小时同步并在启动时立即执行；
    - 本次数量低于当前有效合约 80% 时拒绝写入；
    - 合约连续缺失 2 次后才关闭，单次缺失只标记待确认；
    - 重新出现的合约创建新的有效期记录，不覆盖旧历史；
    - 每分钟幂等键防止重试重复增加缺失次数。
12. worker 会根据目录同步结果记录 `HEALTHY` 或 `DEGRADED`；Binance 临时失败时保留进程并继续周期重试。
13. 已用真实 PostgreSQL 集成测试验证首次上市、重复同步、连续缺失、下线和重新上市完整生命周期。
14. 已引入并固定 Binance 官方 USDⓈ-M Go SDK `v1.14.0`，仅存在于 `internal/binancews`。
15. 已完成全市场 `!miniTicker@arr` WebSocket 适配：
    - 使用 SDK 的 `ConnectMarket`，不建立 public/private 多余连接；
    - 使用 `HTTP_PROXY_URL` 生成 SDK HTTP proxy 配置；
    - SDK 自动生成类型转换为项目 `market.MiniTicker`；
    - 价格和成交量使用 `shopspring/decimal`，不进入 `float64`；
    - 检测无效记录和消费者背压。
16. 已完成 WebSocket supervisor：
    - `last_event_at` 和连接状态；
    - 30 秒无有效行情判定失活；
    - 23.5 小时主动轮换，避开 Binance 连接生命周期上限；
    - 连接、订阅、SDK error、watchdog 和轮换后统一重连；
    - worker 心跳根据行情连接状态进入 `HEALTHY` 或 `DEGRADED`。
17. 已实现并发安全的最新行情内存视图，乱序事件不能覆盖更新事件。
18. 已实现每个合约六小时的有界分钟行情窗口：
    - 自然分钟边界采样；
    - 同一分钟幂等覆盖；
    - 乱序插入仍保持时间有序；
    - 旧事件不能延长保留时间；
    - 过期 symbol 从内存移除。
19. 已实现自然 5 分钟快照：在 `12:05` 写入的 `bucket_time=12:00`，明确表示
    `[12:00, 12:05)`；只接受窗口结束附近 90 秒内的新鲜行情。
20. 已实现 pgx Batch 事务落库，只保存该窗口结束时有效的 universe 合约；滚动 24 小时
    涨幅由 decimal 精确计算，不经过 `float64`。
21. `collection_runs` 会记录每个窗口的预期、实际、缺失数量和缺失 symbol；重复 bucket
    返回已有结果，不产生重复行。
22. worker 重启时从最近六小时的 PostgreSQL 5 分钟快照预热窗口，并从最后一次 run 开始
    登记停机期间无法由 miniTicker 精确回补的窗口。当前采集恢复后健康状态可恢复，历史缺口仍可查询。
23. 已用真实 PostgreSQL 验证快照 numeric 编码、批量事务、缺失记录、bucket 幂等、窗口预热
    和停机缺口登记。
24. 已通过临时 SSH 隧道完成 jmk `7890` 实网 smoke test：官方 SDK 成功连接 Binance
    USDⓈ-M WebSocket 并接收、转换、校验真实全市场 miniTicker；测试未使用或修改 `7891`。
25. 已完成 15 分钟 K 线领域模型与 Binance REST 客户端：
    - 支持 `symbol`、`interval`、`startTime`、`endTime` 和 `limit`；
    - 请求前完成参数校验，非法参数不访问网络；
    - Binance 数组响应直接转换为稳定领域模型；
    - 价格和成交量使用 `shopspring/decimal`，并校验时间边界与 OHLC；
    - 非 2xx 错误保留 HTTP 状态码和响应摘要；
    - 全仓库测试、vet 与新增模块 race 测试通过。
26. 已完成 K 线限速采集与 PostgreSQL 幂等写入：
    - 使用 `golang.org/x/time/rate` 建立 V2 进程级共享请求权重预算；
    - 按 K 线 `limit` 阶梯计算 1/2/5/10 权重，默认 limit 按 500 条处理；
    - 采集器通过领域 source/repository 接口隔离 Binance 与 pgx 类型；
    - 只写入已经完成的 15 分钟 K 线，拒绝错标的、重复和畸形来源数据；
    - pgx Batch 事务按 `(instrument_id, open_time)` upsert，重复回放不产生重复行；
    - 网络错误、418、429、5xx 有限重试，永久参数错误不重试，退避支持取消；
    - 已用隔离 PostgreSQL 17 实例完成重复写、更新、整批拒绝集成测试；
    - 全仓库普通测试、vet 与全仓库 race 测试通过。
27. 已完成 MHR-3 历史回补与缺口恢复：
    - 按完整 15 分钟时间网格读取 PostgreSQL 覆盖，识别首部、中间和尾部缺口；
    - 已完成 UTC 日优先使用 Binance 官方 Vision 日包，SHA-256 校验后才解析；
    - 当前日、归档 404 和零散区间使用 `/fapi/v1/klines`，非 404 归档错误不会被隐藏；
    - 按 symbol 默认 8 并发，REST 继续共用全局权重 limiter；
    - 写入后重新查询覆盖，重复运行完整窗口时不再发网络请求；
    - `collection_runs` 保存每个缺口的 `RECOVERED/PARTIAL/MISSING`、剩余点数和最后错误；
    - jmk 真实空库回补 716 个合约、85,920 个目标点，最终缺口 0、失败 0；
    - jmk 独立临时测试库覆盖空历史、部分历史、中间缺口和重复回补，测试后已删除。
28. 已完成 MHR-4 多周期收益率与质量门禁：
    - 统一计算 15m、1h、4h、24h 收益率，保留实际基准时点、来源、偏差和窗口缺口；
    - 当前价/基准缺失或陈旧、低质量快照、K 线缺口、新上市历史不足和零流动性均产生稳定原因码；
    - 无效收益写为 SQL `NULL`，数据库约束禁止无效记录伪装成零收益；
    - migration 3 使用每 symbol/as_of/version 一行保存四周期 typed return 和 JSON 质量证据；
    - `features` 命令及 worker 共用“增量回补 → 计算 → 幂等保存”pipeline；
    - jmk 完整 K 线边界实算 716 个标的，2,860 个指标有效，4 个因 `BITOUSDT` 零流动性排除；
    - 同一时点重算仍为 716 行和 1 条计算审计；真实 worker smoke 完成后正常停止。

`backfill` 已可执行真实历史回补：

```bash
binance-monitor backfill --env-file /absolute/path/.env.v2
```

默认回补最近 30 小时已完成的 15 分钟 K 线。`BACKFILL_LOOKBACK_HOURS` 不得小于 25，
`BACKFILL_CONCURRENCY` 默认 8、最大 32。输出包括覆盖数、写入数、归档/REST 请求数、
剩余缺口和失败区间；完整审计保存在 PostgreSQL `collection_runs`。

单次补齐行情并计算多周期收益率：

```bash
binance-monitor features --env-file /absolute/path/.env.v2
```

worker 启动后会在每个自然 5 分钟边界后执行同一流水线。默认当前价和基准价最多偏离 5 分钟，
快照最低质量分 75，最近 60 分钟成交额为 0 的标的不产生有效收益。

## 本地启动 V2 基础设施

```bash
cp .env.v2.example .env.v2
```

修改 `.env.v2` 中的 URL-safe PostgreSQL 密码，然后执行：

```bash
make v2-config
make v2-up
```

查看状态：

```bash
docker compose --env-file .env.v2 \
  -p binance-radar-v2 \
  -f compose.v2.yaml ps

curl http://127.0.0.1:8080/health/live
curl http://127.0.0.1:8080/health/ready
```

停止服务但保留数据库卷：

```bash
make v2-down
```

`make v2-down` 不带 `--volumes`，因此不会删除 PostgreSQL 数据。

## 模块依赖规则

后续开发遵守以下约束：

- `internal/v2/*` 可以依赖领域接口和 storage，但 V1 不依赖 V2；
- Binance SDK 类型只能存在于 `internal/binancews` 适配层；
- SQL 只能存在于 migration 或 PostgreSQL repository，不进入信号业务代码；
- worker 负责编排，不实现特征公式或 Telegram 排版；
- API 只读，不直接调用 Binance；
- 每个信号规则有版本，不能通过修改旧数据“更新历史解释”；
- V2 任何出网客户端都必须显式使用 `HTTP_PROXY_URL`，不读取或修改宿主机全局代理。

## 第三方库选型原则

项目不再把“零依赖”作为 V2 目标。存在成熟、通用并且能显著减少基础设施代码的库时，优先使用第三方库，但必须同时满足：

1. 维护活跃，有清晰版本和升级记录；
2. 许可证适合本项目部署方式；
3. 能通过内部接口隔离，不让库的类型蔓延到领域层；
4. 依赖规模和运行代价与实际问题匹配；
5. 关键行为可以测试，并且存在明确的故障与退出语义。

当前选择：

| 能力 | 库 | 边界 |
| --- | --- | --- |
| CLI | Cobra | 只存在于 `internal/cli` 和 command 适配层 |
| PostgreSQL | pgx/v5 | 只存在于 `internal/storage/postgres` |
| Binance WebSocket | Binance 官方 Go SDK v1.14.0 | 只存在于 `internal/binancews` |
| 金融小数 | shopspring/decimal v1.4.0 | 行情领域值，禁止先转 float64 再恢复 |
| REST 权重限速 | golang.org/x/time/rate v0.15.0 | 只存在于 `internal/ratelimit`，通过内部接口注入 Binance 客户端 |

Go 标准库的 `context`、`net/http`、`time`、`encoding/json` 仍然直接使用；它们本身就是稳定的通用基础能力。交易信号、生命周期和数据质量规则属于本项目核心领域，不能交给通用库黑盒处理。

## 下一批开发任务

多周期 Top 5 的专项需求、阶段状态与验收证据统一记录在
[多周期涨幅 Top 5：需求与执行台账](./v2-multi-horizon-top5-plan.md)。

1. 按 Crypto/TradFi 和 15m/1h/4h/24h 生成稳定 Top N；
2. 使用收益率、24h 成交额和 symbol 建立完全确定的并列排序；
3. 保存可复现榜单快照，保证任何无效指标都无法进入榜单；
4. 增加收益率、排名、采集完整率和最近缺口的只读 API。
