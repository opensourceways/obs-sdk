# spec — 可观测契约层（单一事实来源）

> 需求：[opensourceways/backlog#1938](https://github.com/opensourceways/backlog/issues/1938) · 子任务 A：[#2061](https://github.com/opensourceways/backlog/issues/2061)
> 本目录是 **log / metrics 输出格式的唯一权威定义**。`go/` `python/` `java/` `node/` 四个语言 SDK 必须按此实现；不一致时以本目录为准。

## 设计原则（摘自 #2061 范围边界与设计约束）

1. **首期只做 log + metrics，不含 trace**；`trace_id` 字段预留注入位，二期经 context 平滑接入。
2. **SDK 是薄封装，不自研 instrumentation**：指标底层引用各语言官方库（Go→client_golang、Python→prometheus-client、Java→micrometer/actuator、Node→prom-client），SDK 只做装配（中间件 / 通用字段注入 / 命名对齐）。
3. **接口抽象 + Init() 一行装配**：业务代码依赖 SDK 暴露的接口/方法，不依赖具体 instrumentation 实现。
4. **community 双层注入**：日志字段与指标 label 均含 `community`；部署级默认 + 请求级动态覆盖两套场景共用同一注册。

## 目录

| 文件 | 内容 |
| --- | --- |
| [log-format.md](log-format.md) | 结构化日志 JSON 格式、级别、通用字段、kv 传参约定、`trace_id` / `span_id` 预留位 |
| [metrics-format.md](metrics-format.md) | 指标命名前缀、label 规范、`community` 双层注入建模 |
| [common-fields.md](common-fields.md) | 通用字段 `service` / `env` / `instance` / `community` / `request_id` / `trace_id` / `span_id` 的来源与注入规则 |
| [community-values.md](community-values.md) | `community` 取值枚举（`infrastructure` 仓 `service.yaml` 的 communities，随 service.yaml 更新） |

## 语言 SDK 对应关系

| 语言目录 | SDK | log 底层 | metrics 底层 | 用法文档 |
| --- | --- | --- | --- | --- |
| `go/` | obs-sdk-go | stdlib `log/slog` + 自定义 Handler（单行扁平 JSON → stdout） | `client_golang` | [go/README](../go/README.md) |
| `python/` | obs-sdk-python | stdlib `logging`（自定 JSON Formatter） | `prometheus-client` | [python/README](../python/README.md) |
| `java/` | obs-sdk-java | `logback` + logstash JSON encoder | `micrometer` + prometheus registry | [java/README](../java/README.md) |
| `node/` | obs-sdk-node | 自定 JSON serializer（console → stdout） | `prom-client` | [node/README](../node/README.md) |

## community 取值要点（详见 [community-values.md](community-values.md)）

- 遵循 `opensourceways/infrastructure` 的 `service.yaml` 社区枚举：`Ascend / BoostKit / CANN / Common / HiFloat / HPCKit / Infrastructure / Merlin / MindSpore / openEuler / OpenFuyao / openGauss / OpenJiuwen / openLookeng / OpenPangu / OpenUBMC / UnifiedBus / Xihe`。
- 完整列表与重新同步方式见 [community-values.md](community-values.md)。
- 单社区独立部署的服务：`community` = 部署级静态值（环境变量 `OBS_COMMUNITY` 或 Init 配置），日志常驻字段、指标常驻 label。
- 中心化单实例服务多社区：`community` 由请求上下文动态覆盖（路由按社区分发时设置），日志按请求打印、指标按请求打点。
