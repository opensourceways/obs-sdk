# obs-sdk-java

opensourceways 微服务可观测薄封装 SDK 的 Java 实现，契约见根目录 [spec/](../spec/README.md)。
语义与 Go / Python / Node SDK 对齐：

- **community 双层注入**：`service/env/instance` 为部署级 const label，`community` 建模为普通可变 label，
  值取请求上下文覆盖（可信判定点写入），未覆盖回退部署默认 —— 「注册一次两用」。
- **`trace_id` / `span_id` 预留**：首期只保证字段可写可透传，不落 span。
- **日志**：结构化 JSON（logback + logstash JSON encoder，MDC 输出固定键）。
- **指标**：Micrometer + Prometheus registry 薄封装（业务 counter/gauge/histogram）；
  HTTP 服务端指标不重复造轮子，Java 服务走 Spring Boot Actuator + Micrometer 官方 server instrumentation。

## 模块

| 组件 | 说明 |
| --- | --- |
| `context.RequestContext` | 请求级上下文（community/request_id/trace_id/span_id），ThreadLocal 作用域句柄，对齐其它语言的 sdkctx/contextvars/ALS |
| `log.ObsLogging` | 部署默认字段 + 请求覆盖字段写入 SLF4J MDC，由 JSON encoder 输出 |
| `ObsMetrics` | 业务指标装配（common tags + community 动态 label + namespace 前缀） |
| `middleware.ObsFilter` | 可选 Servlet Filter：注入 request_id + 可信判定点解析 community → RequestContext + MDC |

## 构建与测试

JDK 17 + Maven：

```bash
mvn test
```

> 当前开发机无 JDK/Maven，Java 代码未在本机编译运行；已按 Micrometer 1.13 公开 API 编写，
> 由仓库 CI（.github/workflows/ci.yml 的 java job）负责编译 + 跑 `mvn test` 验证。
> 如 CI 暴露问题，以 CI 输出为准修复。

## 指标使用

```java
ObsSdkConfig cfg = ObsSdkConfig.builder()
        .service("review")          // 生产走 OBS_SERVICE 等环境变量，ObsSdkConfig.fromEnvironment()
        .env("test")
        .instance("pod-1")
        .community("openeuler")     // 部署级默认 community
        .build();
ObsMetrics m = ObsMetrics.of(cfg);

// 业务 counter —— community 自动排首位，值取请求上下文覆盖，否则回退默认
ObsMetrics.CounterVec built = m.counter("built_releases", "发布的构建数", "kind");
built.inc(1, "tag");

// 请求上下文内再埋 → 该条 series 的 community 被覆盖为 mindspore
try (RequestContext.Scope scope = RequestContext.push("mindspore", "req-1", null)) {
    built.inc(1, "tag");
}

// gauge / histogram（Timer 自动产出 _seconds_count/_sum，可配桶）
m.gauge("in_flight", "在飞请求数").set(3);
m.histogram("review_duration", "评审耗时", new double[]{0.1, 0.5}).observe(0.05);

// Prometheus text 快照（供自检 / 测试）
String text = m.text();
```

> **命名说明**：Micrometer 会自动为 Counter 追加 `_total`、为 Timer（秒基）追加 `_seconds`，
> 因此 Java 侧传<b>基础名</b>（不带 `_total`/`_seconds` 后缀），导出名与其它语言 SDK 一致
> （如 `built_releases_total`、`review_duration_seconds_count`）。
> 跨服务共享 SDK 时给 `namespace`（如 `"obs"`），导出名变为 `obs_<metric>_total`。

### community 双层注入的日志侧

```java
// 服务启动：
ObsLogging.init(ObsSdkConfig.fromEnvironment());

// 请求处理：先 init() 后每次请求在可信判定点解析后 push 即可
RequestContext.push(community, requestId, traceId);   // try-with-resource 作用域
```

## 集成：Spring Boot Actuator + Micrometer

Java 服务的服务端指标交给 Actuator 暴露（stater 对齐，SDK 不重复埋 HTTP 指标）：

1. 依赖 `micrometer-registry-prometheus` + 引入 `spring-boot-starter-actuator`。
2. 把 SDK 的 registry 暴露成 bean 让 Actuator 托管（`PrometheusMeterRegistry` 会被
   actuator 自动发现并挂到 `/actuator/prometheus`）：

```java
@Bean
public PrometheusMeterRegistry prometheusRegistry(ObsSdkConfig obsConfig) {
    return ObsMetrics.of(obsConfig).meterRegistry();
}
```

3. `application.yml`：`management.endpoints.web.exposure.include: prometheus`。

在接入服务里把 SDK 的 business 指标注册到同一个 `PrometheusMeterRegistry`（上面 bean 的实例），
即可与 Actuator 的服务端指标合并暴露给 AOM 抓取。

## 请求上下文中间件（可选 Servlet Filter）

```java
// Spring Boot 注册，community resolver 必须来自可信判定点（路由前缀/认证主体/白名单），
// 不能裸读 URL/Header 当 community（见 spec/community-values.md）
@Bean
public FilterRegistrationBean<ObsFilter> obsFilter() {
    FilterRegistrationBean<ObsFilter> reg = new FilterRegistrationBean<>();
    reg.setFilter(new ObsFilter(req ->
            req.getRequestURI().startsWith("/mindspore") ? "mindspore" : null));
    reg.addUrlPatterns("/*");
    reg.setOrder(Ordered.HIGHEST_PRECEDENCE);
    return reg;
}
```

## 日志 JSON 输出

把 `examples/logback-json.xml` 拷成接入服务的 logback 配置并引入 `logstash-logback-encoder`，
日志即输出单行 JSON（固定键 `service/env/instance/community/request_id/trace_id/span_id`），例：

```json
{"@timestamp":"2026-09-08T09:00:00.000+08:00","level":"INFO","logger_name":"com.x.ReviewSvc","message":"hello","service":"review","env":"test","instance":"pod-1","community":"openeuler"}
```
