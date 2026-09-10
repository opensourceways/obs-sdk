# AGENTS.md

本文件是给 AI coding agent（Claude Code / opencode / Codex 等）在本仓库工作时的通用指引，
不绑定任何单一工具。人类读者请看 [README.md](README.md) 与各语言 README。

## 仓库是什么

opensourceways 微服务的**可观测薄封装 SDK monorepo**：把「结构化 JSON 日志 + Prometheus 指标」两个能力，
按各语言官方库做一层装配（中间件 / 通用字段注入 / 命名对齐），**不自研 instrumentation**。
输出格式由 [spec/](spec/README.md) 契约层统一约束，四个语言实现必须对齐。

需求：[backlog#1938](https://github.com/opensourceways/backlog/issues/1938) · 子任务：[backlog#2061](https://github.com/opensourceways/backlog/issues/2061)

## 铁律（改代码前必读）

1. **`spec/` 是唯一事实来源**。涉及日志/指标字段名、取值、命名的改动，先改 spec，再改四个语言实现。实现与 spec 不一致时以 spec 为准。
2. **薄封装，不自研 instrumentation**。指标底层一律引用官方库：Go→`client_golang`、Python→`prometheus-client`、Java→`micrometer`+Actuator、Node→`prom-client`。SDK 只做装配。
3. **首期只做 log + metrics，不含 trace**。`trace_id` / `span_id` 是**预留注入位**：字段可写可透传、有值才输出，首期不落 span。改动时不要顺手"补全"成真 trace。
4. **契约改动必须四语言同步**。改一个字段就要动 `spec/` + `go/` `python/` `node/` `java/`，不要只改一处。社区枚举（`community`）例外——它只是文档，随上游刷新即可。
5. **日志只支持 kv 传参，禁止 printf 风格**。`msg` 必须是常量短语，可变数据走键值对。理由：msg 内嵌值会让每行都不同，无法聚合计数，告警规则会静默失效。
6. **高基数值禁止当 metrics label**（`request_id` / `trace_id` / `span_id` 等），会撑爆时序基数。
7. **`community` 只在可信判定点解析**（路由前缀 / 认证主体 / 白名单），**禁止裸读 URL / Header**。

## 目录结构

```
spec/     契约层：log-format / metrics-format / common-fields / community-values（唯一权威定义）
go/       obs-sdk-go     —— log / metrics / sdkctx / middleware(+ginmw)
python/   obs-sdk-python —— obs_sdk/{log,metrics,_context,middleware}
node/     obs-sdk-node   —— lib/{log,metrics,context,middleware}
java/     obs-sdk-java   —— io.opensourceways.obssdk.{ObsSdkConfig,ObsMetrics,log,context,middleware}
```

各语言子目录自管版本（`go.mod` / `pyproject.toml` / `pom.xml` / `package.json`），Git tag 按语言加前缀区分。
**Go 模块的 tag 必须是 `<子目录>/v<版本>` 形式**（如 `go/v1.0.0`）—— Go 工具链按模块根所在子目录解析 tag，
写成 `go-v1.0.0` 这种连字符形式 `go get` 拉不到该模块；其余语言无此约束。

## 契约速查

**日志字段顺序**：`time / level / msg / service / env / instance / community / request_id / trace_id / span_id / logger / error` + 业务字段（扁平，`snake_case`）。
Go 与 Java 按此顺序输出；Python / Node 业务字段在前，但字段名与取值一致。

**`time`**：固定毫秒精度（3 位小数）UTC，以 `Z` 结尾（**不是** `+00:00`，**不是**纳秒）。
**`level`**：小写 `debug` / `info` / `warn` / `error`。
**`logger`**：调用位置 `文件:行号`（调试定位用）。Go/Java 输出，Python/Node 暂不输出（契约中该字段可选）。
**`error`**：错误信息。Go 输出 `err.Error()` 文本（低基数）；Python 等有异常上下文的语言**可含完整 traceback**（多行，JSON 转义为 `\n`，仍是单行 JSON）。**该字段不适合聚合**（取值逐次不同），按错误类型聚合请用 `msg` 常量 + 业务字段。

**community 双层注入**（四种语言同一语义，贯穿日志与指标）：
- `service` / `env` / `instance`：**部署级** const，来自 Init 配置或 `OBS_SERVICE` / `OBS_ENV` / `OBS_INSTANCE` / `OBS_COMMUNITY` 环境变量。
- `community`：普通**可变** label/字段 —— 请求上下文覆盖优先，未覆盖回退部署默认。中心化多社区服务靠它按请求区分。
- `community` 取值枚举见 [spec/community-values.md](spec/community-values.md)，来源是 `opensourceways/infrastructure` 仓的 `service.yaml`（不是值就先去那里查，别自己编）。

## 各语言：怎么加日志与指标

### Go（`go/`）

```go
import (
    obslog "github.com/opensourceways/obs-sdk/go/log"
    obsmetrics "github.com/opensourceways/obs-sdk/go/metrics"
    obshttpmw "github.com/opensourceways/obs-sdk/go/middleware"
    "github.com/opensourceways/obs-sdk/go/sdkctx"
)

// 启动时 Init 一次，之后全进程用包级函数（kratos v3 形状，无需在每个调用点绑定 logger）
obslog.Init(obslog.Config{Service: "review", Env: "test", Instance: "pod-1", Community: "openEuler"})

// 日志：msg 常量 + kv 交替（禁止 printf）
obslog.Info("job done", "event", "release", "issue", "2061")
obslog.ErrorContext(ctx, "get account failed", "user_id", uid, "error", err) // ctx 里的请求字段自动附加

// 指标：注册一次；community label 自动排首位，取请求覆盖或回退默认
m := obsmetrics.New(obsmetrics.Config{Service: "review", Env: "test", Instance: "pod-1", Community: "openEuler"})
built := m.NewCounterVec("built_releases", "发布的构建数", "kind")
built.Inc("tag")
built.IncWithContext(ctx, "tag") // 请求内 → 该条 series 的 community 取 ctx 覆盖值

// 中间件：注入 request_id + 可信判定点解析 community + 记 obs_http_server_*
h := obshttpmw.New(obshttpmw.Options{
    Metrics:          m,
    ResolveCommunity: func(r *http.Request) string { /* "/mindspore" → "mindspore" */ return "" },
}).Then(myHandler)
// gin：r.Use(ginmw.Middleware(ginmw.Options{Metrics: m}))

// 请求内覆盖（trace_id/span_id 为二期预留）
ctx = sdkctx.WithCommunity(ctx, "mindspore")
ctx = sdkctx.WithRequestID(ctx, "req-123")
```

指标注册：`NewCounterVec` / `NewGaugeVec` / `NewHistogramVec` / `NewHistogramVecWithBuckets`，名称为**基础名**（不带 `_total` / `_seconds`）；暴露用 `m.Handler()`。

### Python（`python/`）

```python
from obs_sdk import log, metrics
from obs_sdk import _context

log.init(service="review", env="test", instance="pod-1", community="openEuler")  # 进程内幂等
logger = log.get_logger(__name__)
logger.info("job done", extra={"event": "release", "issue": "2061"})   # 业务字段走 extra=
log.info("job done", extra={"issue": "2061"})                          # 或便捷函数

metrics.init(service="review", env="test", instance="pod-1", community="openEuler")
built = metrics.counter("built_releases", "发布的构建数", ["kind"])      # community 自动补
built.inc(1, kind="tag")

# 请求上下文：中间件之外也可手工 bind（community 必须在可信判定点解析）
with _context.bind(community="mindspore", request_id="req-1"):
    logger.info("scoped")            # 自动带 community/request_id
    built.inc(1, kind="tag")         # 该 series community 被覆盖

# /metrics 暴露
# return Response(content=metrics.generate_text(), media_type=metrics.content_type())
```

框架适配：`middleware.fastapi_wrap(app, resolver=...)`、`flask_middleware(app, resolver=...)`、`DjangoMiddleware`（MIDDLEWARE 列表加 `obs_sdk.middleware.DjangoMiddleware`）。
多注册表场景直接 `metrics.Metrics(...)` 而非模块级单例。

### Node（`node/`）

```js
const obs = require('obs-sdk-node');   // { log, metrics, context, middleware }

obs.log.init({ service: 'review', env: 'test', instance: 'pod-1', community: 'openEuler' });
obs.log.info('job done', { event: 'release', issue: '2061' });   // 业务字段走第二个对象参数

const m = new obs.metrics.Metrics({ service: 'review', env: 'test', instance: 'pod-1', community: 'openEuler' });
const built = m.counter('built_releases_total', '发布的构建数', ['kind']);  // prom-client 要最终名，SDK 不改名
built.inc(1, { kind: 'tag' });

// 中间件：注入 request_id + 解析 community + 记 obs_http_server_*
const { makeMiddleware, metricsRouteHandler } = obs.middleware;
app.use(makeMiddleware({ metrics: m, resolveCommunity: (req) => undefined }));
app.get('/metrics', metricsRouteHandler(m));

// 请求内覆盖
obs.context.bindRequest({ community: 'mindspore', requestId: 'req-1' }, () => {
  obs.log.info('scoped');
});
```

### Java（`java/`）

```java
ObsSdkConfig cfg = ObsSdkConfig.builder()
        .service("review").env("test").instance("pod-1").community("openEuler")
        .build();                      // 生产用 ObsSdkConfig.fromEnvironment() 读 OBS_*

ObsLogging.init(cfg);                  // 部署级字段写入 MDC
ObsMetrics m = ObsMetrics.of(cfg);

ObsMetrics.CounterVec built = m.counter("built_releases", "发布的构建数", "kind");
built.inc("tag");                      // 基础名（不带 _total/_seconds），Micrometer 自动补后缀

// 请求处理：可信判定点解析后 push（try-with-resources）
try (RequestContext.Scope scope = RequestContext.push("mindspore", "req-1", null)) {
    log.info("job done");              // SLF4J；MDC 里的请求字段由 JSON encoder 输出
    built.inc("tag");                  // 该 series community 取覆盖值
}
```

日志 JSON 输出**必须**用 [java/examples/logback-json.xml](java/examples/logback-json.xml)：它只挂 SDK 的
`ObsJsonProvider`，由 SDK 保证字段名/顺序/时间格式/级别小写/异常堆栈。**不要退回 encoder 自带 provider**——
`<logLevel/>` 只能输出大写、字段名不可配、且不配 `<stackTrace/>` 会整条丢弃 throwable。
该 provider 需要 `logstash-logback-encoder` + `jackson-core`（SDK 内为 `provided`，接入服务运行时提供）。

Servlet 接入（可选）：`new ObsFilter(req -> resolveCommunity(req))`，或 Spring Boot 注册 `FilterRegistrationBean<ObsFilter>`。
Java 的**服务端指标**不重复埋点——走 Spring Boot Actuator + Micrometer 官方 server instrumentation，把
`m.meterRegistry()` 暴露成 bean 由 Actuator 托管。

## 构建与测试

```bash
cd go     && go vet ./... && go test -race ./...
cd python && pytest                       # 需 .venv 内已装 prometheus_client 等依赖
cd node   && npm install && npm test
cd java   && mvn test                     # 需 JDK 17 + Maven
```

工具链版本对齐 CI（[.github/workflows/ci.yml](.github/workflows/ci.yml)）：**Go 的版本以 `go/go.mod` 的 `go` 指令为准**
（CI 用 `go-version-file` 读它，不要在文档里硬编码具体版本）、Python 3.10、Node 20、Java 17。
本机没有系统级 JDK/Maven 时，可把 `JAVA_HOME` / `PATH` 指向自装工具链；Java 的最终验证以 CI 为准。

## 改动约定

- **改了契约就四语言同步**，并在各语言补 UT。断言要落在**真实输出**上，不要只断言 MDC / 中间变量——
  Java 的字段名/级别/时区问题（以及 throwable 被丢弃）正是因为只测了 MDC 才漏到合入后的。
- Java 的 `ObsJsonProviderTest` 会**直接加载 `examples/logback-json.xml`**（测试工作目录是 `java/`），
  改动样例配置会反映到测试里。
- 新增/改动 `community` 取值前先同步 [spec/community-values.md](spec/community-values.md)，其来源与重新同步命令见该文件。
- 发现代码里的 bug：**指出来，但不要顺手修**（超出当前任务范围的改动先问）。
