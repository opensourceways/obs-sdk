# obs-sdk-go

opensourceways 微服务可观测薄封装 SDK 的 Go 实现，契约见 [spec/](../spec/README.md)。

- **日志**：`log` package —— 加锁单行 JSON 写 stdout（`encoding/json` marshal map），字段规范见 spec/log-format.md
- **指标**：`metrics` package —— `client_golang` 薄封装（counter/gauge/histogram），label 规范见 spec/metrics-format.md
- **请求上下文**：`sdkctx` —— `context.Context` 承载 `community/request_id/trace_id`
- **中间件**：`middleware`（net/http）+ `middleware/ginmw`（gin）—— 注入 request_id、可信判定点解析 community、记 `obs_http_server_*` 指标
- **community 双层注入**：`service/env/instance` 部署级 const label；`community` 普通可变 label —— 请求上下文覆盖，未覆盖回退部署默认（`OBS_*` 环境变量）

## 目录

| package | 说明 |
| --- | --- |
| [sdkctx](sdkctx/context.go) | `Request{Community,RequestID,TraceID}` + `WithCommunity/WithRequestID/WithTraceID` + `From/Community/RequestID/TraceID` |
| [log](log/) | `New(cfg Config) *Logger`，方法 `Info/Warn/Debug/Error(msg, kv...)`，`With(kv...)`、`WithRequest(ctx)` |
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

// 日志：字段空则回退 OBS_SERVICE / OBS_ENV / OBS_INSTANCE / OBS_COMMUNITY
l := obslog.New(obslog.Config{Service: "review", Env: "test",
    Instance: "pod-1", Community: "openeuler"})
l.Info("job done", "event", "release", "issue", "2061")

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

// 业务请求内覆盖 community / request_id / trace_id（trace_id 预留位，不落 span）
ctx := sdkctx.WithCommunity(r.Context(), "mindspore")
l.WithRequest(ctx).Info("scoped")
built.IncWithContext(ctx, "tag")
```

## 验证

```bash
go test ./...
go vet ./...
```
