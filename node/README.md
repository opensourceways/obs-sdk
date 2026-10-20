# obs-sdk-node

opensourceways 微服务可观测薄封装 SDK 的 Node 实现，契约见 [spec/](../spec/README.md)。

- **日志**：`lib/log` —— 单行 JSON 写 stream（默认 stdout），固定字段顺序 `time/level/msg/service/env/instance/community/[request_id]/[trace_id]/[span_id]/logger/error/业务字段`
- **log4js 适配**：`lib/log.makeLog4jsLayout()` —— 不引 log4js 依赖，直接作为自定义 layout 使用
- **指标**：`lib/metrics` —— prom-client 薄封装（counter/gauge/histogram）
- **请求上下文**：`lib/context` —— `AsyncLocalStorage` 承载 `community/request_id/trace_id/span_id`（后两者为二期 trace 预留位）
- **中间件**：`lib/middleware` —— Express/通用 HTTP 中间件（注入 request_id + 可信判定点解析 community + 记 `http_server_*`）
- **community 双层注入**：`service/env/instance` 常驻；`community` 可变 —— 请求上下文覆盖，未覆盖回退部署默认（`OBS_*` 环境变量）
- **类型声明**：`index.d.ts` 随包发布（`package.json` 的 `types` 字段），TS 消费方无需自建 ambient 声明

## 用法

```js
const obs = require('obs-sdk-node');      // index.js：{ log, metrics, context, middleware }

// ---- 日志 ----
obs.log.init({ service: 'review', env: 'test', instance: 'pod-1', community: 'openeuler' });
obs.log.info('job done', { event: 'release', issue: '2061' });
obs.log.error('job failed', { error: new Error('boom'), job: 'release' });

// 字段规则：
// - msg 必须是常量短语；业务值走第二个参数的扁平 kv
// - Error 放 error 键，序列化为整段 stack；其余对象/数组序列化为字符串，保证顶层不嵌套
// - 保留键（service/env/msg/level/time/...）不会被业务字段覆盖
// - logger 默认取调用位置末两级路径:行号；高吞吐场景可 init({disableSource: true}) 关闭

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
// 业务 app 里 use(mw) 即可：注入 request_id + push 请求上下文 + 请求结束记 http_server_* 指标
// /metrics 暴露：app.get('/metrics', metricsRouteHandler(m));
```

请求级覆盖（context 作用域内日志 / 指标自动带覆盖 community 与 request_id/trace_id/span_id）：

```js
obs.context.bindRequest({ community: 'mindspore', requestId: 'req-1', traceId: 'trace-x' }, () => {
  obs.log.info('scoped');          // 单行 JSON 带 community="mindspore", request_id="req-1"
  built.inc(1, { kind: 'tag' });   // 该 series community="mindspore"
});
```

## log4js 接入

用 `makeLog4jsLayout()` 接管 log4js 的输出，存量 `logger.info('...', arg)` 调用无需改动：

```js
const log4js = require('log4js');
const obs = require('obs-sdk-node');

obs.log.init({ service: 'etherpad-lite', community: process.env.OBS_COMMUNITY });

log4js.configure({
  appenders: {
    // 注册为 log4js 的 'obs' layout，返回 { type: 'obs' }
    stdout: { type: 'stdout', layout: obs.log.registerLog4jsLayout(log4js) },
  },
  categories: {
    default: {
      appenders: ['stdout'],
      level: process.env.LOGLEVEL || 'info',
      // logger 字段来自 event.callStack；不开启则不输出该字段。
      enableCallStack: true,
    },
  },
});
```

layout 约定：`data[0]` 为 msg；其余 plain object 平铺为业务字段；`Error` 进 `error`；
其他参数按 `util.format` 拼进 msg，兼容存量 printf 风格日志。

## 验证

```bash
cd node && npm install && npm test
cd node && npm run typecheck   # 校验 index.d.ts 与公开 API 用法一致
```
