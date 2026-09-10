# 日志格式契约 — 结构化 JSON

> 唯一权威定义：各语言 SDK 的 log 输出必须与本文件一致。
> 采集链路：服务 stdout（结构化 JSON）→ 华为云 log-agent（`allContainers: true` 全量采集）→ LTS。格式统一是 LTS 内可检索/结构化/告警的前提。

## 输出形态

- 每行一条日志，单行 JSON（**不 pretty**，不换行），通过 stdout 输出。
- 字符集 UTF-8。
- 时间字段 UTC，RFC 3339 / ISO-8601，**固定毫秒精度（3 位小数）**：`2026-09-08T07:12:34.567Z`。
  固定位数（而非纳秒可变位数）是为了四语言输出形态一致、LTS 侧正则提取稳定；秒级时间戳已足够支撑检索与告警，纳秒对日志场景无实际增益。

## 顶层字段

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `time` | string | 是 | RFC 3339 UTC，输出时刻 |
| `level` | string | 是 | 枚举见下 |
| `msg` | string | 是 | 人类可读消息（错误场景为该错误摘要） |
| `service` | string | 是 | 服务名（= `infrastructure` 仓 `service.yaml` 的服务名 / go module 子目录名），Init 时注入，常驻 |
| `env` | string | 是 | 部署环境 `prod/test/preview/staging`，Init 注入，常驻 |
| `instance` | string | 是 | 实例标识（k8s pod 名/IP），Init 注入，常驻 |
| `community` | string | 是 | 社区标识。部署级默认注入；请求级覆盖见 common-fields.md |
| `request_id` | string | 否* | 单请求关联 ID；无则输出空串或省略（*见下） |
| `trace_id` | string | 否* | **预留位**：二期 trace 接入前始终为空/省略，见下 |
| `span_id` | string | 否* | **预留位**：二期 trace 接入前始终为空/省略，见下 |
| `logger` | string | 否 | 产生日志的调用位置（形如 `service/webhook.go:51`，调试定位用） |
| `error` | string | 否 | 错误信息。Go 等无异常上下文的语言输出 `err.Error()` 原因链文本（低基数、可检索）；Python 等有异常上下文的语言**可含完整 traceback**（多行，JSON 转义为 `\n`，仍是单行 JSON）。仅在携带错误对象/异常时输出 |
| 其余 | — | 否 | 业务字段平铺在顶层（扁平 JSON），键名小写下划线（`snake_case`），禁止嵌套对象优先平铺 |

> **request_id / trace_id / span_id 预留约定**：契约字段表里三者语义必留，但取值可为空。两种实现都接受：(a) 输出 `"trace_id":""` / `"span_id":""` 空串；(b) 为空时**省略该键**。Go SDK 统一采用 (b)「有值才输出」以减少噪音——但 `trace_id`/`span_id` 语义位必须存在（SDK 须有注入这两个字段的能力），二期 trace 一接即可见。

> **error 字段不适合聚合**：带 traceback 的语言（Python 等）该字段取值逐次不同，不要对它做 count / group by。
> 需要按错误类型聚合时，用 `msg`（常量短语）+ 业务字段（如 `error_code` / `event`）另作维度。

## level 枚举

`debug` / `info` / `warn` / `error`。（`fatal` 由调用方决定是否需要 os.Exit，日志层不强制）

## 示例（合法行）

首期（trace_id / span_id 为空 → 省略该键）：

```json
{"time":"2026-09-08T07:12:34.567Z","level":"info","msg":"handle github webhook","service":"robot-universal-review","env":"test","instance":"review-7f9c5d8b66-abcde","community":"openEuler","request_id":"req_01J8XK","logger":"service/webhook.go:51","event":"pull_request","action":"opened"}
```

二期（trace 接入后，同一条日志格式零返工即带 trace_id / span_id）：

```json
{"time":"2026-09-08T07:12:34.567Z","level":"error","msg":"get account failed","service":"robot-universal-review","env":"test","instance":"review-7f9c5d8b66-abcde","community":"openEuler","request_id":"req_01J8XK","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7","logger":"service/todo.go:51","error":"get account from db: connection refused","user_id":"u-123"}
```

## 字段传参约定：msg 常量 + kv 承载数据

`msg` 是**人类可读的常量短语**（如 `"get account failed"`），不得把随请求变化的取值（用户 ID、订单号、错误详情）格式化进 `msg`。
所有可查询的维度一律通过 **kv** 传入，平铺为顶层字段：

```go
log.ErrorContext(ctx, "get account failed", "user_id", uid, "error", err)
// → {"msg":"get account failed","user_id":"u-123","error":"get account from db: connection refused"}
```

理由：`msg` 一旦嵌值，同一事件的每行 `msg` 都不同 → LTS 无法按 `msg` 聚合/计数，日志告警规则失效；且改文案即静默破坏已配置的检索。

**禁止 printf 风格**（`log.Errorf("failed for user %s", uid)`）：Go `log/slog` 无 printf 变体，四语言 SDK **只提供 kv 形式**，与主流生态（stdlib `log/slog`、kratos v3）一致。

## 推荐：从上下文附加请求级字段

所有语言 SDK 都应支持从请求上下文（Go `context.Context` 等价物）读取并附加 `request_id`、`community`、`trace_id`、`span_id`（后两者预留）四个字段。调用风格二选一或兼具：

1. **参数式**：`InfoContext(ctx, msg, kv...)` —— 每次显式传 context。Go SDK 采用此式，形状对齐 kratos v3 / stdlib `log/slog`。
2. **绑定式**：`WithRequestContext(ctx)` 返回绑定请求字段的 logger —— 此后调用自动带上。

要求：**同一条日志内 `request_id`/`community`/`trace_id`/`span_id` 必须唯一、确定**；context 缺省时回退到 Init 注入的静态默认值（community 尤其如此）。

## LTS 检索约定

- LTS 结构化字段名 = 本表顶层字段名；日志检索时按 `service` / `community` / `level` / `request_id` 过滤。
- 服务内业务字段建议在 LTS 里再做一次字段提取（正则/JSON 解析），键名同日志顶层键。
