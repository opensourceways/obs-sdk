# 日志格式契约 — 结构化 JSON

> 唯一权威定义：各语言 SDK 的 log 输出必须与本文件一致。
> 采集链路：服务 stdout（结构化 JSON）→ 华为云 log-agent（`allContainers: true` 全量采集）→ LTS。格式统一是 LTS 内可检索/结构化/告警的前提。

## 输出形态

- 每行一条日志，单行 JSON（**不 pretty**，不换行），通过 stdout 输出。
- 字符集 UTF-8。
- 时间字段 UTC，RFC 3339 / ISO-8601，带毫秒以上精度：`2026-09-08T07:12:34.567Z` 或 `.567890Z`（尽力而为，纳秒精度佳）。

## 顶层字段

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `time` | string | 是 | RFC 3339 UTC，输出时刻 |
| `level` | string | 是 | 枚举见下 |
| `msg` | string | 是 | 人类可读消息（错误场景为该错误摘要） |
| `service` | string | 是 | 服务名（= service.md 微服务名 / go module 子目录名），Init 时注入，常驻 |
| `env` | string | 是 | 部署环境 `prod/test/preview/staging`，Init 注入，常驻 |
| `instance` | string | 是 | 实例标识（k8s pod 名/IP），Init 注入，常驻 |
| `community` | string | 是 | 社区标识。部署级默认注入；请求级覆盖见 common-fields.md |
| `request_id` | string | 否* | 单请求关联 ID；无则输出空串或省略（*见下） |
| `trace_id` | string | 否* | **预留位**：二期 trace 接入前始终为空/省略，见下 |
| `logger` | string | 否 | 产生日志的 logger/包名（调试定位用） |
| `error` | string/object | 否 | 错误信息（仅 error 级推荐） |
| 其余 | — | 否 | 业务字段平铺在顶层（扁平 JSON），键名小写下划线（`snake_case`），禁止嵌套对象优先平铺 |

> **request_id / trace_id 预留约定**：契约字段表里两者语义必留，但取值可为空。两种实现都接受：(a) 输出 `"request_id":""` / `"trace_id":""` 空串；(b) 为空时**省略该键**。SDK 内部统一为「有值才输出」可减少噪音——但 `trace_id` 语义位必须存在（SDK 须有注入该字段的能力），二期 trace 一接即可见。

## level 枚举

`debug` / `info` / `warn` / `error`。（`fatal` 由调用方决定是否需要 os.Exit，日志层不强制）

## 示例（合法行）

```json
{"time":"2026-09-08T07:12:34.567890Z","level":"info","msg":"handle github webhook","service":"robot-universal-review","env":"test","instance":"review-7f9c5d8b66-abcde","community":"openEuler","request_id":"req_01J8XK","trace_id":"","event":"pull_request","action":"opened"}
```

## 推荐：从上下文附加请求级字段

所有语言 SDK 都应支持从请求上下文（Go `context.Context` 等价物）读取并附加 `request_id`、`community`、`trace_id`（预留）三个字段。SDK 暴露两种调用风格之一或兼具：

1. **绑定式**：`logger.WithRequestContext(ctx).Info(...)` —— 用 context 值绑定一个带请求字段的 logger，此后调用自动带上。
2. **参数式**：`logger.Info(ctx, msg, kv...)` —— 每次显式传 context。

要求：**同一条日志内 `request_id`/`community`/`trace_id` 必须唯一、确定**；context 缺省时回退到 Init 注入的静态默认值（community 尤其如此）。

## LTS 检索约定

- LTS 结构化字段名 = 本表顶层字段名；日志检索时按 `service` / `community` / `level` / `request_id` 过滤。
- 服务内业务字段建议在 LTS 里再做一次字段提取（正则/JSON 解析），键名同日志顶层键。
