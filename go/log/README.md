# log package

单行 JSON 结构化日志（`encoding/json` marshal map → stdout，加锁保证单行完整）。
字段按 [spec/log-format.md](../../spec/log-format.md) / [common-fields.md](../../spec/common-fields.md)：
`service/env/instance/community` 常驻 + 请求级 `request_id/trace_id`/business kv。

```go
l := obslog.New(obslog.Config{Service: "review", Community: "openeuler"})
l.Info("job done", "event", "release")                 // 单行 JSON → stdout
l.WithRequest(ctx).Info("scoped")                      // ctx 里 community/request_id 覆盖常驻字段
```

`ParseLevel` 支持 `debug/info/warn/error`；空字段回退 `OBS_SERVICE/OBS_ENV/OBS_INSTANCE/OBS_COMMUNITY`。
详细用法见 [../README.md](../README.md)。
