# Binance USDⓈ-M 涨幅榜 Telegram Reporter

这是一个纯 Go 实现的定时行情报告服务。它在北京时间每天
`00:00 / 04:00 / 08:00 / 12:00 / 16:00 / 20:00` 读取 Binance USDⓈ-M
永续合约，分别生成 TradFi 与 Crypto 板块的滚动 24 小时涨幅前 5，
补充上榜标的简介和资料链接，然后推送到 Telegram。

`24:00` 与次日 `00:00` 是同一时刻，因此每天实际有 6 个不重复的推送时点。

> V2 市场雷达正在 `feature/v2-market-radar` 分支开发。V2 的产品、业务架构、
> 技术架构和执行计划参见 [总体产品与开发 Roadmap](docs/v2-roadmap.md) 与
> [docs/README.md](docs/README.md)。当前 V1 行为保持不变。

V2 当前已完成 15 分钟 K 线采集、PostgreSQL 幂等存储、至少 30 小时历史回补、
15m/1h/4h/24h 收益率和质量门禁，以及 Crypto/TradFi 分板块确定性 Top 5。
已完成 UTC 日优先使用 Binance 官方校验归档，当前日和缺口使用 Futures REST；`features`
命令和 worker 共用五分钟“回补 → 收益 → 排名”流水线，`rankings --as-of` 支持历史时点重放。执行方法、
环境变量和验收结果见 [V2 开发进度](docs/v2-development.md) 与
[多周期 Top 5 执行台账](docs/v2-multi-horizon-top5-plan.md)。候选深度特征、可解释评分和
信号状态机的后续开发以 [MHR-9 执行台账](docs/v2-signal-lifecycle-plan.md) 为准。
候选池编码前的七天分布基线、指标定义和冷启动约束见
[R4-A0 候选指标分布分析](docs/v2-candidate-distribution-analysis.md)。

## 功能

- 自动识别 Binance TradFi 与 Crypto 永续合约。
- 分别输出两张榜单：
  - TradFi 涨幅前 5；
  - Crypto 涨幅前 5。
- 显示标的、合约、最新成交价和滚动 24 小时涨跌幅。
- 为上榜标的附加中文简介和权威资料链接。
- Telegram HTML 等宽排版，并根据消息长度自动分段。
- HTTP 超时、限流及服务端错误自动重试。
- 使用状态文件记录最后成功时点，避免进程重启造成重复推送。
- 整点执行失败后，在宽限期内每分钟重试。
- 支持 Telegram 论坛群组的指定话题。
- 资产资料通过 `go:embed` 编入二进制，部署时不依赖项目源文件。
- V1 核心业务保持轻量；统一 CLI 使用 Cobra。V2 按边界引入 pgx、Binance 官方 SDK 和
  shopspring/decimal，具体版本与隔离规则见 `docs/v2-development.md`。

## 数据口径

行情来自：

```text
GET https://fapi.binance.com/fapi/v1/ticker/24hr
```

合约信息和分类来自：

```text
GET https://fapi.binance.com/fapi/v1/exchangeInfo
```

默认筛选规则：

1. `status == TRADING`；
2. `quoteAsset == USDT`；
3. `contractType == TRADIFI_PERPETUAL` 时归入 TradFi；
4. `contractType == PERPETUAL` 时归入 Crypto；
5. 交割合约、停牌或结算中的合约不会进入榜单；
6. Telegram 涨幅榜只收录 `priceChangePercent > 0` 的标的；
7. 上涨标的不足 5 个时，不会使用持平或下跌标的凑数。

这里的“涨跌幅”是 Binance `ticker/24hr` 返回的滚动 24 小时变化，不是从上一个
四小时报告时点开始计算的区间变化。

## 报告示例

Telegram 中的榜单采用等宽文本块：

```text
Binance USDⓈ-M 24h 涨幅榜
数据时间：2026-07-27 00:00:20（北京时间）

TradFi 涨幅前 5｜标的 / 合约 / 最新价 / 24h
1. KORU       KORUUSDT               19.35    +4.821%
2. INTW       INTWUSDT               21.70    +4.528%

Crypto 涨幅前 5｜代币 / 合约 / 最新价 / 24h
1. EUL        EULUSDT               2.4992   +71.944%
2. BTC        BTCUSDT            118,200.00    +2.315%
```

排行榜之后会发送 TradFi 和 Crypto 的上榜标的简介、资料来源及必要的产品说明。

## 项目结构

```text
.
├── cmd/binance-monitor/       # CLI 入口和信号处理
├── internal/
│   ├── app/                   # 单次报告任务编排
│   ├── binance/               # Binance 公共接口和响应解析
│   ├── catalog/               # 内嵌标的简介资料库
│   ├── candidateanalysis/     # R4 候选指标只读分布研究
│   ├── config/                # 环境变量与 .env 配置
│   ├── httpjson/              # JSON HTTP、超时和重试
│   ├── model/                 # 核心数据结构
│   ├── ranking/               # 涨跌榜筛选与排序
│   ├── report/                # Telegram HTML 报告渲染
│   ├── scheduler/             # 定时、宽限期和推送去重
│   └── telegram/              # Telegram Bot API 客户端
├── .env.example
├── Dockerfile
├── compose.yaml
├── Makefile
└── go.mod
```

## 环境要求

- Go 1.26 或更高版本；或
- Docker 与 Docker Compose。

Binance 公共行情不需要 API Key。Telegram 推送需要 Bot Token 和目标 Chat ID。

## 本地构建

```bash
git clone https://github.com/danielsonggit/binance_monitor.git
cd binance_monitor
make check
make build
```

生成的二进制位于：

```text
bin/binance-monitor
```

也可以直接使用 Go 命令：

```bash
go test ./...
go vet ./...
go build -trimpath -o bin/binance-monitor ./cmd/binance-monitor
```

## 配置 Telegram

### 1. 创建 Bot

在 Telegram 中联系 `@BotFather`，使用 `/newbot` 创建 Bot，并取得 Bot Token。

### 2. 设置目标会话

- 私聊：先向 Bot 发送一条消息。
- 群组：将 Bot 加入群组。
- 频道：将 Bot 设为管理员并授予发消息权限。

发送测试消息后访问：

```text
https://api.telegram.org/bot<TELEGRAM_BOT_TOKEN>/getUpdates
```

从响应中的 `chat.id` 取得 `TELEGRAM_CHAT_ID`。超级群组和频道的 Chat ID 通常以
`-100` 开头。

### 3. 创建配置文件

```bash
cp .env.example .env
```

至少填写：

```dotenv
TELEGRAM_BOT_TOKEN=123456789:your_bot_token
TELEGRAM_CHAT_ID=-1001234567890
```

如果目标是开启话题功能的论坛群组，并且只想发送到某个话题：

```dotenv
TELEGRAM_MESSAGE_THREAD_ID=123
```

不要把 `.env`、Bot Token 或其他密钥提交到 Git。

## 运行方式

### 生成预览

抓取真实行情并在终端打印完整报告，不调用 Telegram：

```bash
./bin/binance-monitor --dry-run
```

或者：

```bash
make dry-run
```

### 立即推送一次

```bash
./bin/binance-monitor --once
```

### 按时点常驻运行

```bash
./bin/binance-monitor --daemon
```

不传 `--once`、`--dry-run` 时默认也是常驻模式：

```bash
./bin/binance-monitor
```

### 使用其他环境变量文件

```bash
./bin/binance-monitor --env-file /absolute/path/reporter.env --once
```

### 调试日志

```bash
./bin/binance-monitor --verbose --once
```

## 完整配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TELEGRAM_BOT_TOKEN` | 空 | Telegram Bot Token；实际推送时必填 |
| `TELEGRAM_CHAT_ID` | 空 | 私聊、群组或频道 ID；实际推送时必填 |
| `TELEGRAM_MESSAGE_THREAD_ID` | 空 | 可选的论坛群组话题 ID |
| `REPORT_TIMEZONE` | `Asia/Shanghai` | 调度和报告显示时区 |
| `REPORT_HOURS` | `0,4,8,12,16,20` | 每日执行小时，取值 `0` 到 `23` |
| `SCHEDULE_GRACE_MINUTES` | `10` | 整点后允许补跑及重试的分钟数 |
| `QUOTE_ASSETS` | `USDT` | 结算资产；可配置为 `USDT,USDC` |
| `TOP_N` | `5` | 每个榜单最多显示多少个标的 |
| `BINANCE_FAPI_BASE_URL` | `https://fapi.binance.com` | Binance USDⓈ-M API 基础地址 |
| `HTTP_TIMEOUT_SECONDS` | `20` | 单次 HTTP 请求超时 |
| `HTTP_MAX_RETRIES` | `3` | 网络、HTTP 429 或 5xx 的最大尝试次数 |
| `STATE_FILE` | `state/scheduler.json` | 最后成功推送时点的状态文件 |

`.env` 文件支持空行、以 `#` 开头的注释、`KEY=VALUE` 以及单引号或双引号包裹的值。
系统环境变量优先级高于 `.env`，已有环境变量不会被覆盖。

## 调度、补跑和去重

服务每分钟检查一次是否进入配置的报告时点。默认情况下：

- `04:00` 到 `04:09` 属于 `04:00` 报告的执行宽限期；
- 如果 `04:00` 第一次发送失败，服务会在后续分钟再次尝试；
- 只有全部 Telegram 消息发送完成，才会写入成功状态；
- 成功状态写入 `state/scheduler.json`；
- 同一个 `04:00` 时点成功后，即使进程在 `04:05` 重启，也不会重复发送；
- 如果服务到 `04:10` 以后才启动，不会补发 `04:00` 报告，而是等待下一个时点。

状态文件采用同目录临时文件加原子重命名写入。不要让两个进程共享同一
`TELEGRAM_CHAT_ID` 却使用不同状态文件，否则两个进程都会推送。

## 标的简介资料库

内置资料位于：

```text
internal/catalog/assets.json
```

该文件会通过 `go:embed` 编入可执行文件。简介查找以基础资产简称为键，例如：

```json
{
  "BTC": {
    "name": "Bitcoin",
    "description": "经过核验的一句话简介。",
    "url": "https://权威资料地址"
  }
}
```

如果新上榜标的尚未收录：

- TradFi 会根据 Binance 的 `underlyingType` 和板块标签生成保守说明；
- Crypto 会明确写出“本地资料库尚无经过核验的项目简介”；
- 程序不会根据相同或相似的代币简称猜测项目身份。

可以在不重新编译的情况下使用外部资料库：

```bash
./bin/binance-monitor \
  --catalog /absolute/path/assets.json \
  --dry-run
```

外部文件会完全替换内置资料库，而不是与内置内容合并。修改简介后应先运行
`--dry-run` 检查链接、排版和消息长度。

## Docker 部署

创建 `.env` 后运行：

```bash
docker compose up -d --build
docker compose logs -f
```

停止服务：

```bash
docker compose down
```

`compose.yaml` 使用 `reporter-state` 命名卷挂载容器的 `/app/state`，容器更新或重启
后仍能保留推送去重记录。容器以非 root 用户和只读根文件系统运行，只有状态卷可写。

查看状态卷：

```bash
docker volume inspect binance_monitor_reporter-state
```

实际卷名会带 Compose 项目前缀；如果目录名或 `--project-name` 改变，请先运行
`docker volume ls` 确认。

查看容器内的单次预览：

```bash
docker compose run --rm reporter --dry-run
```

镜像采用多阶段构建：第一阶段编译静态 Go 二进制，运行阶段只包含 Alpine、TLS
根证书和应用二进制，并使用非 root 用户运行。

## systemd 部署示例

先把二进制和配置文件放到固定目录，例如：

```text
/opt/binance-monitor/binance-monitor
/opt/binance-monitor/reporter.env
/opt/binance-monitor/state/
```

创建 `/etc/systemd/system/binance-monitor.service`：

```ini
[Unit]
Description=Binance USD-M Telegram Reporter
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=binance-monitor
Group=binance-monitor
WorkingDirectory=/opt/binance-monitor
EnvironmentFile=/opt/binance-monitor/reporter.env
ExecStart=/opt/binance-monitor/binance-monitor --daemon
Restart=on-failure
RestartSec=10
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

然后执行：

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now binance-monitor
sudo journalctl -u binance-monitor -f
```

## 测试与质量检查

```bash
make check
```

等价命令：

```bash
go test ./...
go vet ./...
```

测试覆盖：

- TradFi/Crypto 合约分类；
- USDT、状态和合约类型过滤；
- 无效行情过滤；
- 正负方向和成交额并列排序；
- 资产简介命中及未知标的兜底；
- 报告结构、价格格式和 Telegram 长度限制；
- Telegram HTML 请求体和话题 ID；
- 调度宽限期；
- 状态文件读写与同一时点去重。

## Binance HTTP 451

如果日志显示：

```text
HTTP 451
Service unavailable from a restricted location
```

这不是程序故障，而是 Binance 根据请求出口所在地区拒绝服务。本项目不会尝试绕过
地域限制。应将服务部署在能够合法访问 Binance Futures 公共接口的地区，或者把
`BINANCE_FAPI_BASE_URL` 配置为你自己维护的合规数据转发服务。

在当前机器不确定是否可访问时，先运行：

```bash
./bin/binance-monitor --dry-run
```

只有预览能成功获取真实行情后，再启动 Telegram 常驻推送。

## 常见问题

### 为什么不是每天 7 次？

因为 `24:00` 就是次日 `00:00`。把两者都配置会造成同一时刻重复推送。

### 为什么涨幅榜可能少于 5 个？

涨幅榜只接受正涨跌幅。如果该板块当时上涨标的少于 5 个，程序不会拿持平或下跌标的填充。

### 为什么新代币没有详细介绍？

代币简称可能重名，自动猜测容易把项目写错。未核验标的只输出明确的兜底说明；核实
项目身份和权威链接后，再加入 `internal/catalog/assets.json` 或外部资料库。

### 修改 `.env` 后需要重启吗？

需要。配置只在进程启动时读取。

### Binance API Key 应该填在哪里？

不需要。这个服务只读取公开市场数据，不访问账户，也不执行交易。
