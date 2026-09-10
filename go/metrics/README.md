# metrics package

`client_golang` 薄封装。注册时 `service/env/instance` 为 const label，`community` 为普通可变 label
（请求上下文覆盖，未覆盖回退默认），见 [spec/metrics-format.md](../../spec/metrics-format.md)。

```go
m := obsmetrics.New(obsmetrics.Config{Service: "review", Env: "test",
    Instance: "pod-1", Community: "openeuler"})       // 可选 Namespace 前缀
built := m.NewCounterVec("built_releases", "发布的构建数", "kind")
built.Inc("tag")                                      // community=部署默认
built.IncWithContext(ctx, "tag")                      // ctx 里有覆盖则取覆盖值

m.NewGaugeVec("in_flight", "在飞请求数").Set(3)
m.NewHistogramVec("latency", "耗时", "api").Observe(0.2)

http.Handle("/metrics", m.Handler())                  // promhttp 暴露
```

默认注册 go / process collectors（`Config.IncludeProcess=false` 可关）。详细用法见 [../README.md](../README.md)。
