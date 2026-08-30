# jmk V2 shadow services

MHR-7 在 jmk 上采用以下边界：

- PostgreSQL 17 继续由 `binance-radar-v2` Compose project 管理，只发布到
  `127.0.0.1:54329`；
- worker 与只读 API 使用 user-systemd 和静态 Go 二进制；
- `run-v2` 强制所有 V2 Binance 出网使用宿主机 `127.0.0.1:7890`；
- 不安装 reporter unit，不会在影子阶段发送 Telegram；
- watchdog 是独立健康进程，默认 `WATCHDOG_DRY_RUN=true`，不会调用 Telegram；
- V1 的 `binance-monitor.service` 和 7891 不属于本部署。

部署目录固定为 `/home/daniel/services/binance-radar-v2`，user unit 安装到
`/home/daniel/.config/systemd/user`。API 默认只监听 `127.0.0.1:18080`。

安装 watchdog 前创建唯一可写状态目录：

```bash
install -d -m 0700 /home/daniel/services/binance-radar-v2/state
```

运行器在 `.env.v2` 之后加载权限为 `0600` 的独立 `watchdog.env`。专用
`WATCHDOG_TELEGRAM_*` 变量优先；未设置时依次回退到 `TELEGRAM_BOT_TOKEN`、
`TELEGRAM_CHAT_IDS/TELEGRAM_CHAT_ID` 和主题 ID。不要把 token 写进仓库或 systemd unit。
真实启用必须把 `binance-radar-v2-watchdog-live.conf.example` 安装为 systemd drop-in 后重启，
不能直接修改仓库中的安全默认值。

切换 live 前可在临时环境覆盖下发送一条明确的通道测试消息：

```bash
WATCHDOG_DRY_RUN=false ./run-v2 watchdog --test-notification
```

常用检查：

```bash
systemctl --user status binance-radar-v2-worker.service
systemctl --user status binance-radar-v2-api.service
systemctl --user status binance-radar-v2-watchdog.service
journalctl --user -u binance-radar-v2-worker.service -n 100 --no-pager
journalctl --user -u binance-radar-v2-watchdog.service -n 100 --no-pager
curl http://127.0.0.1:18080/health/ready
```

停止影子服务不会影响 PostgreSQL 或 V1：

```bash
systemctl --user disable --now binance-radar-v2-worker.service
systemctl --user disable --now binance-radar-v2-api.service
systemctl --user disable --now binance-radar-v2-watchdog.service
```
