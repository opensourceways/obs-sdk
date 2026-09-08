# obs-sdk-node

opensourceways 微服务可观测薄封装 SDK 的 Node 实现，契约见 [spec/](../spec/README.md)。

- **日志**：`lib/log` —— 单行 JSON 写 stream（默认 stdout）
- **指标**：`lib/metrics` —— prom-client 薄封装（counter/gauge/histogram）
- **请求上下文**：`lib/context` —— `AsyncLocalStorage` 承载 `community/request_id/trace_id`
- **中间件**：`lib/middleware` —— Express/通用 HTTP 中间件（注入 request_id + 可信判定点解析 community + 记 `obs_http_server_*`）
- **community 双层注入**：`service/env/instance` 常驻；`community` 可变 —— 请求上下文覆盖，未覆盖回退部署默认（`OBS_*` 环境变量）

## 用法

```js
const obs = require('obs-sdk-node');      // index.js：{ log, metrics, context, middleware }

// ---- 日志 ----
obs.log.init({ service: 'review', env: 'test', instance: 'pod-1', community: 'openeuler' });
obs.log.info('job done', { event: 'release', issue: '2061' });

// ---- 指标（prom-client 需显式 _total 等最终名，本 SDK 不做改名） ----
const m = new obs.metrics.Metrics({ service: 'review', env: 'test',
    instance: 'pod-1', community: 'openeuler' });
const built = m.counter('built_releases_total', '发布的构建数', ['kind']);
built.inc(1, { kind: 'tag' });
m.gauge('in_flight', '在飞请求数').set(3);
m.histogram('review_duration_seconds', '评审耗时').observe(0.2);

// ---- HTTP 服务端指标（中间件自动登记） ----
const { makeMiddleware, metricsRouteHandler } = obs.middleware;
const mw = makeMiddleware({
  metrics: m,                      // 不传则用默认 Metrics
  // community 必须在可信判定点解析（路由前缀/认证主体/白名单），见 spec/community-values.md
  resolveCommunity: (req) => (req.url.startsWith('/mindspore') ? 'mindspore' : undefined),
});
// 业务 app 里 use(mw) 即可：注入 request_id + push 请求上下文 + 请求结束记 obs_http_server_* 指标
// /metrics 暴露：app.get('/metrics', metricsRouteHandler(m));
```

请求级覆盖（context 作用域内日志 / 指标自动带覆盖 community 与 request_id/trace_id）：

```js
obs.context.bindRequest({ community: 'mindspore', requestId: 'req-1', traceId: 'trace-x' }, () => {
  obs.log.info('scoped');          // 单行 JSON 带 community="mindspore", request_id="req-1"
  built.inc(1, { kind: 'tag' });   // 该 series community="mindspore"
});
```

## 验证

```bash
cd node && npm install && npm test
```
