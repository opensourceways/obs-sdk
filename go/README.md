# obs-sdk-go

opensourceways 微服务可观测薄封装 SDK 的 Go 实现，契约见 [spec/](../spec/README.md)。

- **日志**：`log` package —— 基于 stdlib `log/slog` 的自定义 Handler，单行扁平 JSON 写 stdout，字段规范见 spec/log-format.md
  - **只提供 kv 传参**（`msg` 常量 + 交替 `key, value`），**无 printf 变体** —— 与 `log/slog`、kratos v3 一致；可查询的数据必须走字段
  - 包级 API 形状对齐 kratos v3：`Init` 之后直接 `log.Info(...)` / `log.InfoContext(ctx, ...)`，无需在每个调用点先绑定 logger
  - `error` 值统一序列化为 `err.Error()` 文本（`encoding/json` 会把多数错误渲染成 `{}`）
- **指标**：`metrics` package —— `client_golang` 薄封装（counter/gauge/histogram），label 规范见 spec/metrics-format.md
- **请求上下文**：`sdkctx` —— `context.Context` 承载 `community/request_id/trace_id/span_id`
- **中间件**：`middleware`（net/http）+ `middleware/ginmw`（gin）—— 注入 request_id、可信判定点解析 community、记 `obs_http_server_*` 指标
- **community 双层注入**：`service/env/instance` 部署级 const label；`community` 普通可变 label —— 请求上下文覆盖，未覆盖回退部署默认（`OBS_*` 环境变量）

## 目录

| package | 说明 |
| --- | --- |
| [sdkctx](sdkctx/context.go) | `Request{Community,RequestID,TraceID,SpanID}` + `WithCommunity/WithRequestID/WithTraceID/WithSpanID` + `From/Community/RequestID/TraceID/SpanID` |
| [log](log/) | `Init(cfg Config)`；包级 `Info/Warn/Debug/Error(msg, kv...)` + `InfoContext(ctx, msg, kv...)` + `Log/LogAttrs`；二级 API `New(cfg) *slog.Logger`、`With(kv...)`、`SetDefault/Default` |
| [metrics](metrics/) | `New(cfg Config) *Metrics`；`NewCounterVec/NewGaugeVec/NewHistogramVec(WithBuckets)(name, help, businessLabels...)`；`Handler()`（promhttp） |
| [middleware](middleware/) | `New(opts Options).Then(http.Handler)`，net/http |
| [middleware/ginmw](middleware/ginmw/) | `Middleware(opts Options) gin.HandlerFunc` |

## 用法

```go
import (
    obslog "github.com/opensourceways/obs-sdk/go/log"
    obsmetrics "github.com/opensourceways/obs-sdk/go/metrics"
    obshttpmw "github.com/opensourceways/obs-sdk/go/middleware"
    "github.com/opensourceways/obs-sdk/go/sdkctx"
)

// 日志：Init 一次，之后全进程直接用包级函数。
// 字段空则回退 OBS_SERVICE / OBS_ENV / OBS_INSTANCE / OBS_COMMUNITY
obslog.Init(obslog.Config{Service: "review", Env: "test",
    Instance: "pod-1", Community: "openeuler"})

// 无请求上下文（如启动阶段）：字段 = 常驻字段 + kv
obslog.Info("job done", "event", "release", "issue", "2061")

// msg 是常量短语，可变数据一律走 kv（禁止 printf 风格格式化进 msg）
// 二级 API 仍可用：自定义 writer / 预置字段 / 接入自定义 slog 装配
l := obslog.New(obslog.Config{Service: "review"})
l.With("component", "webhook").Info("job done", "issue", "2061")

// 指标：注册一次，community 值按请求覆盖或回退部署默认
m := obsmetrics.New(obsmetrics.Config{Service: "review", Env: "test",
    Instance: "pod-1", Community: "openeuler"})
built := m.NewCounterVec("built_releases", "发布的构建数", "kind")
built.Inc("tag")

// 中间件：可信判定点解析 community → 写入 context（未覆盖则回退默认）
h := obshttpmw.New(obshttpmw.Options{
    Metrics: m,
    ResolveCommunity: func(r *http.Request) string {
        if strings.HasPrefix(r.URL.Path, "/mindspore") {
            return "mindspore"
        }
        return ""
    },
}).Then(myHandler)
http.ListenAndServe(":8080", h)

// gin 变体：import "github.com/opensourceways/obs-sdk/go/middleware/ginmw"
// r.Use(ginmw.Middleware(ginmw.Options{Metrics: m}))

// 业务请求内覆盖 community / request_id / trace_id / span_id
//（trace_id / span_id 为二期预留注入位，首期恒空、有值才输出）
ctx := sdkctx.WithCommunity(r.Context(), "mindspore")
ctx = sdkctx.WithRequestID(ctx, "req-123")

// 日志：request_id / community 覆盖 / trace_id 自动附加，无需先绑定 logger
obslog.ErrorContext(ctx, "get account failed", "user_id", uid, "error", err)
// 指标：community label 取覆盖值
built.IncWithContext(ctx, "tag")
```

## 验证

```bash
go test ./...
go vet ./...
```
