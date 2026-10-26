# OTLP 映射 — 与 OpenTelemetry 语义约定的对齐表

> 本文件是 [spec/](README.md) 契约的**映射附录**，不是新的契约。
> 字段的权威定义仍在 [log-format.md](log-format.md) / [metrics-format.md](metrics-format.md) /
> [common-fields.md](common-fields.md)；本文件只回答一个问题：
> **现有落盘字段，将来接 OTLP 时分别落到哪个 OTel 语义键上。** 两者冲突时以主契约为准。

## 0. 为什么只需要一张映射表

"对齐 OTel"可以拆成三层，成本差一个量级：

| 层 | 做什么 | 成本 | 本仓库是否做 |
| --- | --- | --- | --- |
| **语义对齐** | 在本文件登记"字段 → OTel 语义键"的对应关系 | 改文档 | ✅ 本文件 |
| **出口对齐** | 由 OTel Collector 把 stdout JSON / `/metrics` 翻译成 OTLP | 一份采集配置，零 SDK 改动 | 需要时再做 |
| **形态对齐** | 改 SDK，直接输出 OTLP LogRecord / OTLP Metrics | 四语言改代码，且会破坏现有 LTS 结构化字段与大盘查询 | ❌ 不做 |

现有落盘形态本身已经适合机械翻译，这是本文件能成立的前提：

- 单行扁平 JSON、字段名固定、键名 `snake_case`；
- `trace_id` / `span_id` 取值是 W3C trace context 的十六进制串（32 / 16 位）；
- 指标名已经是 Prometheus 约定形状，且中间件指标名恰好等于 semconv 的翻译结果（见 §4）。

因此**不需要动任何 SDK**，缺的只是下面这些对应关系。

## 1. 版本锚定

语义约定仍在演进（尤其 `gen_ai.*`），**不锁版本，映射表会静默失效**。本文件锚定：

| 对象 | 版本 / 提交 | 说明 |
| --- | --- | --- |
| `open-telemetry/semantic-conventions` | **v1.44.0**（2026-08-04） | 资源属性、日志、HTTP 指标 |
| `open-telemetry/semantic-conventions-genai` | commit `9ffc7e0`（2026-10-21） | `gen_ai.*` 已**迁出主仓**，独立维护、无 tag，只能按 commit 锚定 |

v1.44.0 下与本契约相关的几个现状，直接影响映射表：

- `deployment.environment` **已废弃** → 用 `deployment.environment.name`；
- `code.filepath` / `code.lineno` **已废弃** → 用 `code.file.path` / `code.line.number`；
- `event.name` **属性已废弃** → 事件名改放 LogRecord 的 `EventName` 字段（不是 attributes）；
- 主仓中的 `gen_ai.*` 全部标记为 deprecated 并迁往上述 genai 仓，**以 genai 仓定义为准**。

> 升级锚定版本时，只需核对本文件受影响的表格行；主契约（log/metrics/common-fields）不动。

## 2. 部署级字段 → Resource

`service` / `env` / `instance` 是**进程级常量**，对应 OTel 的 Resource（资源属性），不是日志属性、也不是指标 label。

| 本契约字段 | OTel 键 | 稳定性 | 翻译规则 |
| --- | --- | --- | --- |
| `service` | `service.name` | Stable | 原值直传（如 `robot-universal-review`） |
| `env` | `deployment.environment.name` | Stable | 值域建议 `production` / `staging` / `test` / `development`；我们的 `prod` → `production`，其余原样，`preview` 无标准值、保留（该属性允许自定义值） |
| `instance` | `service.instance.id` | Stable | 原值直传（pod 名满足"同服务内唯一"） |
| `community` | `osw.community`（**属性，不是 Resource**） | 自定义 | 见下 |

**`community` 不能进 Resource。** 它是请求级可变的（[common-fields.md](common-fields.md) 的双层注入）：
中心化多社区服务一旦把它写进 Resource，该进程产出的**所有**日志/span/指标都会带同一个社区。
它只能作为 log / span attribute 与 metric datapoint attribute。也不要用 `service.namespace` 代替——那同样是 Resource 级。

> 自定义属性统一 `osw.` 前缀（opensourceways 的简写），避免与未来标准键撞名。
> `telemetry.sdk.name` / `telemetry.sdk.language` / `telemetry.sdk.version` 由 exporter 自动补，SDK 不输出。

## 3. 日志字段 → LogRecord

OTel Logs 数据模型把一条日志拆成**固定字段**（时间 / 级别 / body / trace 上下文 / 事件名）+ **attributes**。
本契约的平铺 JSON 按此拆分：

| 本契约字段 | OTLP LogRecord | 稳定性 | 翻译规则 |
| --- | --- | --- | --- |
| `time` | `timeUnixNano` | Stable | 毫秒 → 纳秒，×10⁶（`2026-10-26T08:12:33.421Z` → `1793002353421000000`） |
| `level` | `severityNumber` + `severityText` | Stable | 见下表；`severityText` 转大写，**只在翻译层转，落盘保持小写** |
| `msg` | `body`（string value） | Stable | 原值直传 |
| `trace_id` | `traceId`（**LogRecord 原生字段**） | Stable | 32 位 hex → 16 字节；**不得放进 attributes** |
| `span_id` | `spanId`（**LogRecord 原生字段**） | Stable | 16 位 hex → 8 字节 |
| `logger` | `code.file.path` + `code.line.number` | Stable | 按**最后一个** `:` 切分：`service/webhook.go:51` → `service/webhook.go` + `51`（行号转 int） |
| `error` | `exception.message`（+ `exception.stacktrace`） | Stable | Go 只有 message；Python 的 traceback 整段进 `exception.stacktrace`，最后一行进 `exception.message` |
| `community` | attribute `osw.community` | 自定义 | 原值直传 |
| `request_id` | attribute `osw.request_id` | 自定义 | OTel 无 request id 标准键 |
| `event` | **LogRecord `eventName`** | Stable | 低基数事件名（`release` / `pull_request`）；**不要**再塞 `event.name` 属性（已废弃） |
| 其余业务字段 | attributes（同名） | 自定义 | 键名保持 `snake_case`；高基数键（`issue`、`user_id`）照样入 attributes，但只入日志 |

级别映射（OTLP `SeverityNumber`）：

| 本契约 `level` | `severityNumber` | `severityText` |
| --- | --- | --- |
| `debug` | 5 | `DEBUG` |
| `info` | 9 | `INFO` |
| `warn` | 13 | `WARN` |
| `error` | 17 | `ERROR` |

**`trace_id` / `span_id` 是最容易翻错的一处**：落盘时它们是普通字符串字段，但 OTLP 里是一等字段。
若翻译时当成普通 attribute 写进 `attributes`，日志将**永远关联不上 trace**——这正是二期接 trace 的核心价值。
我们当前的十六进制格式与 W3C trace context 一致，值不需要改，只需放对位置。

### 建议新增（可选）：`error_type`

`error` 是逐次不同的文本，无法聚合（见 [log-format.md](log-format.md)）。OTel 的 `exception.type` 需要一个
**低基数类型名**（如 `*url.Error`、`PermissionError`），恰好补上这个缺口：

| 新增字段 | OTLP | 说明 |
| --- | --- | --- |
| `error_type`（可选） | `exception.type` | 建议由 SDK 从错误对象取类型名；不改变现有 `error` 字段 |

同时指标侧可补 `error.type`（Stable，要求低基数），见 §4。

### 翻译示例

```jsonc
// 落盘（不变）
{"time":"2026-10-26T08:12:33.421Z","level":"info","msg":"job done",
 "service":"review","env":"test","instance":"pod-1","community":"openEuler",
 "request_id":"req-123","logger":"handler/review.go:88","event":"release","issue":"2061"}

// → OTLP LogRecord（OTLP/JSON 编码）
{
  "timeUnixNano": "1793002353421000000",
  "severityNumber": 9,
  "severityText": "INFO",
  "body": { "stringValue": "job done" },
  "eventName": "release",
  "attributes": [
    { "key": "osw.community",    "value": { "stringValue": "openEuler" } },
    { "key": "osw.request_id",   "value": { "stringValue": "req-123" } },
    { "key": "code.file.path",   "value": { "stringValue": "handler/review.go" } },
    { "key": "code.line.number", "value": { "intValue": "88" } },
    { "key": "issue",            "value": { "stringValue": "2061" } }
  ]
}
// resource: service.name="review", deployment.environment.name="test",
//           service.instance.id="pod-1", telemetry.sdk.language="go"
```

## 4. 指标 → OTel 指标语义

Prometheus 与 OTel 的命名是可机械互转的：点 → 下划线、单位后缀、counter 加 `_total`。
本契约的中间件指标**已经是 semconv 的翻译结果**：

| 本契约指标（Prometheus wire） | OTel semconv | 稳定性 | 说明 |
| --- | --- | --- | --- |
| `http_server_request_duration_seconds` | `http.server.request.duration`（Histogram，`s`） | Stable | **名字已对齐，无需改动** |
| `http_server_requests_total` | **无对应指标** | — | semconv 不定义请求计数 counter（计数由 duration histogram 的 `count` 得出）。不要自造 `http.server.request.count`；导出 OTLP 时省略，Prometheus 侧保留 |
| 业务指标 `<service>_xxx_total` | 无标准 | — | 保持原名，作为自定义指标导出 |

label 与属性：

| 本契约 label | OTel 属性 | 稳定性 | 说明 |
| --- | --- | --- | --- |
| `service` / `env` / `instance` | Resource（同 §2） | Stable | 平移，不留在 datapoint attributes |
| `community` | `osw.community` | 自定义 | 原值直传 |
| `method` | `http.request.method` | Stable | 值用大写（`GET`） |
| `path` | `http.route` | Stable | 值必须是**路由模板**（`/items/{id}`），本契约已强制 |
| `status_code` | `http.response.status_code` | Stable | ⚠️ semconv 为 **int**，Prometheus label 是 string，翻译时转类型 |
| （建议新增） | `error.type` | Stable | 失败请求的归一化错误类型，低基数；比按 `status_code` 分组更适合告警 |

注意：

- **`http_server_requests_total` 不是 semconv 指标**。它的计数语义与 `duration` 的 `count` 重复，
  将来若以 OTLP 为主出口，应逐步只保留 histogram，避免同一事实两套指标。
- **Java 的名差异在 OTLP 侧同样存在**：Actuator 输出 `http_server_requests_seconds` /
  `status` / `uri`，与 Go/Python/Node 的 `http_server_request_duration_seconds` /
  `status_code` / `path` 不同。这是 [metrics-format.md](metrics-format.md)「多语言同构边界」的既定结果，
  SDK **不做名字映射**；跨语言查询一律对齐通用 label（`service` / `env` / `instance` / `community`）。
- Prometheus 3 支持 UTF-8 指标名，但本仓库**不使用**点号命名——保持 legacy 名字，兼容现有抓取与大盘。

## 5. `gen_ai.*`（二期预留，本期不实现）

若将来机器人服务需要观测 LLM 调用，按 genai 仓当前定义登记如下（**本期不埋点，仅登记避免返工**）：

| 用途 | 属性键 | 稳定性 |
| --- | --- | --- |
| 操作类型 | `gen_ai.operation.name`（`chat` / `execute_tool` / `invoke_agent` …） | Development |
| 提供方 | `gen_ai.provider.name` | Development |
| 模型 | `gen_ai.request.model` / `gen_ai.response.model` | Development |
| Token 用量 | `gen_ai.usage.input_tokens` / `gen_ai.usage.output_tokens`（span 属性） | Development |
| 结束原因 | `gen_ai.response.finish_reasons` | Development |
| Agent | `gen_ai.agent.name` / `gen_ai.agent.id` | Development |
| 会话 | `gen_ai.conversation.id` | Development |

指标侧（Counter / Histogram，`{token}` / `s`）：

- `gen_ai.client.inference.usage.input_tokens` / `gen_ai.client.inference.usage.output_tokens`
- `gen_ai.client.operation.duration`
- `gen_ai.server.time_to_first_token` / `gen_ai.server.time_per_output_token`

**警告**：`gen_ai.conversation.id`、工具调用 ID、prompt 内容都是高基数，**只能进日志或 span attributes，
禁止作为 metric label**（与 [metrics-format.md](metrics-format.md) 的高基数禁令同一条）。

## 6. 出口怎么接（示意，不在本期实现）

两种现有暴露形态都已是 Collector 能吃的输入，因此"留出口"= 两份采集配置，而不是 SDK 功能：

- **指标**：Collector `prometheus` receiver 抓 `/metrics` → `otlphttp` exporter。现有 `honor_labels: true`
  等抓取规则原样复用（`honor_labels` 只影响 Prometheus 侧 label 冲突，与 OTLP 无关）。
- **日志**：Collector `filelog` receiver 读容器 stdout → `json_parser` → `transform` 按 §2/§3 改名、切分、
  转类型 → `otlphttp` exporter。四语言共用一份配置。

落地时需实际验证两点：`filelog` 对多行 JSON 的 `body` 处理，以及 `transform` 里 hex → bytes 的 `traceId` 转换。

## 7. 不做的事（红线）

1. **不把落盘字段改成 dotted 命名**（如 `http.request.method`）——会同时破坏 LTS 结构化字段、现有 Grafana 查询与四语言一致性。
2. **不把 `community` 放进 Resource**，也不借用 `service.namespace`。
3. **不把 `request_id` / `trace_id` / `span_id` 等高基数值当 metric label**（OTLP 后端同样会被撑爆）。
4. **不把 `trace_id` / `span_id` 当普通 attribute**，必须落到 LogRecord 原生字段。
5. **不硬造 semconv 没有的指标名**（如 `http.server.request.count`）。
6. 业务字段保持 `snake_case` 平铺，仅加反域名前缀用于我们特有维度（`osw.*`）。

## 8. 维护

- 升级 `semantic-conventions` 锚定版本时：核对 §2–§4 中被改名的键（deprecated 会在 registry 中标注 "Replaced by"），
  更新 §1 的版本表；主契约不动。
- `gen_ai.*` 仍在 Development 阶段且已独立出版节奏，接入前必须重新核对 genai 仓的当前 commit，
  不要直接照抄任一厂商文档（各厂商对旧名 `gen_ai.system` / `gen_ai.usage.prompt_tokens` 的支持程度不一）。
