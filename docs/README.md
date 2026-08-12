# Binance Market Radar V2 文档

本目录描述 V2 的产品边界、业务流程、技术架构和实施顺序。V2 不是对 V1
定时涨幅榜的简单加字段，而是把系统升级为持续采集、识别机会、跟踪信号并复盘效果的
市场雷达。

## 文档索引

- [产品需求与业务架构](./v2-product-requirements.md)
- [技术架构与数据设计](./v2-technical-architecture.md)
- [执行计划](./v2-execution-plan.md)
- [开发进度与运行方式](./v2-development.md)
- [多周期涨幅 Top 5：需求与执行台账](./v2-multi-horizon-top5-plan.md)
- [MHR-8：V2 独立健康监控与故障通知](./v2-watchdog-plan.md)

## 当前决策摘要

1. 覆盖全部处于交易状态的 Binance USDⓈ-M 永续合约，包括 Crypto 与 TradFi。
2. 后台持续计算 15 分钟、1 小时、4 小时和 24 小时指标；Telegram 只发送有行动价值的状态变化和定时报表。
3. Telegram 暂不推送“跌幅 Top 5”，但系统仍采集负收益数据，用于风险判断和信号失效检测。
4. 使用 PostgreSQL 保存历史行情、信号和评估结果；V2 首期不引入 Redis、Kafka 或自动交易。
5. Go 代码采用模块化单体、多个运行角色；Binance REST 保留自有轻量客户端，WebSocket 使用官方 Go SDK 并封装在适配层后面。
6. jmk 上的 V1 保持运行；V2 PostgreSQL 使用独立 Docker 卷，worker/API 使用独立 user-systemd 单元。V2 出网仅通过 `127.0.0.1:7890`，不占用 `7891`。

多周期 Top 5 的需求、实现状态和验收证据以专项执行台账为准。

文档中的信号阈值是冷启动默认值，不是已经回测证明有效的交易参数。系统上线收集足够样本后，必须根据命中率、收益分布、MFE、MAE 和不同市场状态重新校准。
