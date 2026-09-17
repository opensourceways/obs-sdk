# obs-sdk — 微服务可观测薄封装 SDK（monorepo）

> 需求：[opensourceways/backlog#1938](https://github.com/opensourceways/backlog/issues/1938) 微服务可观测建设 · 子任务：[#2061](https://github.com/opensourceways/backlog/issues/2061)

统一薄封装 SDK：**结构化 JSON 日志 + Prometheus 指标**两个能力，内部引用各语言官方库（不自研 instrumentation），
输出格式由 [spec/](spec/README.md) 契约层统一约束（单一事实来源）。

## 目录结构
- [spec/](spec/README.md) — 契约层（日志 JSON schema、通用字段、指标命名/label、community 双层注入、trace_id 预留）——单一事实来源
- [go/](go/README.md) — obs-sdk-go（encoding/json 单行 JSON 日志 + client_golang 指标 + http/gin 中间件）
- [python/](python/README.md) — obs-sdk-python（logging JSON Formatter + prometheus-client + FastAPI/Flask/Django 适配）
- [java/](java/README.md) — obs-sdk-java（logback + logstash JSON encoder + micrometer/prometheus registry）
- [node/](node/README.md) — obs-sdk-node（JSON serializer + prom-client + express 中间件）

各语言子目录自管版本（go.mod / pyproject.toml / pom.xml / package.json），Git tag 按语言加前缀区分。
Go 模块的 tag 必须是 `<子目录>/v<版本>` 形式（如 `go/v1.0.0`）—— Go 工具链按模块根所在子目录解析 tag，
写成 `go-v1.0.0` 这种连字符形式 `go get` 拉不到该模块；其余语言无此约束。

## 通用能力（四种语言对齐）

| 能力 | 说明 |
| --- | --- |
| community 双层注入 | `service/env/instance` 部署级 const；`community` 可变 label/字段：请求级可信判定点覆盖，未覆盖回退部署默认（`OBS_*` 环境变量） |
| 请求上下文 | Go `sdkctx`(context.Context)、Python `contextvars`、Node `AsyncLocalStorage`、Java `RequestContext`(ThreadLocal) |
| trace_id 预留 | 字段可写可透传，首期不落 span |
| 服务端指标 | Go/Python/Node 由 SDK 中间件埋 `obs_http_server_*`；Java 走 Actuator + Micrometer 官方 server instrumentation（不重复埋点） |

## 验证

四语言 UT + CI：`.github/workflows/ci.yml`（Go/Python/Node 本机已通过；Java 无本地 JDK，由 CI 的 maven job 编译验证）。
