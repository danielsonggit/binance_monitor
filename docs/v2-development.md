# Binance Market Radar V2：开发进度

更新时间：2026-08-09

当前分支：`feature/v2-market-radar`

## 当前阶段

- Phase 0：产品与架构设计已完成；
- Phase 1：数据库与采集底座进行中；
- Phase 2 及以后：尚未开始。

## 已完成的 Phase 1 能力

1. CLI 已统一使用 Cobra；V1 默认 CLI 和部署行为保持兼容，显式 `binance-monitor v1` 也可进入 V1。
2. Cobra 命令树注册了 `migrate`、`worker`、`api`、`backfill` 四个独立 V2 角色。
3. V2 配置位于 `internal/v2/config`，不把数据库和 Web 配置混入 V1。
4. PostgreSQL 访问位于 `internal/storage/postgres`，使用 pgx/v5 连接池。
5. migration 使用 Go embed 打入二进制，具有 advisory lock、事务、版本和 checksum 防篡改。
6. 第一版 schema 包含：
   - 合约有效期 `instruments`；
   - 分区表 `market_snapshots_5m`；
   - 分区表 `klines_15m`；
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
18. 已实现每个合约两小时的有界分钟行情窗口：
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
22. worker 重启时从最近两小时的 PostgreSQL 5 分钟快照预热窗口，并从最后一次 run 开始
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

`backfill` 当前只有命令入口，会明确返回“下一批实现”，不会假装已经采集历史数据。

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

Go 标准库的 `context`、`net/http`、`time`、`encoding/json` 仍然直接使用；它们本身就是稳定的通用基础能力。交易信号、生命周期和数据质量规则属于本项目核心领域，不能交给通用库黑盒处理。

## 下一批开发任务

多周期 Top 5 的专项需求、阶段状态与验收证据统一记录在
[多周期涨幅 Top 5：需求与执行台账](./v2-multi-horizon-top5-plan.md)。

1. 实现共享权重限速、已完成 K 线过滤与 PostgreSQL 幂等批写；
2. 使用已完成 K 线补充重启后的精确历史，miniTicker 缺口只作质量记录，不伪造回补；
3. 实现 15m/1h/4h/24h 收益率和数据完整度门禁；
4. 增加采集完整率和最近缺口的只读健康 API。
