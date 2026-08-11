# jmk V2 shadow services

MHR-7 在 jmk 上采用以下边界：

- PostgreSQL 17 继续由 `binance-radar-v2` Compose project 管理，只发布到
  `127.0.0.1:54329`；
- worker 与只读 API 使用 user-systemd 和静态 Go 二进制；
- `run-v2` 强制所有 V2 Binance 出网使用宿主机 `127.0.0.1:7890`；
- 不安装 reporter unit，不会在影子阶段发送 Telegram；
- V1 的 `binance-monitor.service` 和 7891 不属于本部署。

部署目录固定为 `/home/daniel/services/binance-radar-v2`，user unit 安装到
`/home/daniel/.config/systemd/user`。API 默认只监听 `127.0.0.1:18080`。

常用检查：

```bash
systemctl --user status binance-radar-v2-worker.service
systemctl --user status binance-radar-v2-api.service
journalctl --user -u binance-radar-v2-worker.service -n 100 --no-pager
curl http://127.0.0.1:18080/health/ready
```

停止影子服务不会影响 PostgreSQL 或 V1：

```bash
systemctl --user disable --now binance-radar-v2-worker.service
systemctl --user disable --now binance-radar-v2-api.service
```
