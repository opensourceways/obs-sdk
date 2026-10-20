// 类型声明使用示例，仅用于 tsc 校验（不参与运行时测试）。
// 每个公开 API 至少调用一次，保证 index.d.ts 与实现同步。

import obs = require('../..');

const logger: obs.Log = obs.log.init({
  service: 'demo',
  env: 'test',
  instance: 'pod-1',
  community: 'openeuler',
  level: 'debug',
  disableSource: true,
});

logger.debug('debug msg', { retry: 1 });
logger.info('job done', { event: 'release', issue: 2061 });
logger.warn('slow response', { elapsed_ms: 1200 });
logger.error('job failed', { error: new Error('boom'), job: 'release' });

const layout = logger.makeLog4jsLayout({ disableSource: true });
const line: string = layout({ data: ['job %s', 'done'], callStack: '', level: 'INFO' });
void line;

const layoutConfig: obs.Log4jsLayoutConfig = logger.registerLog4jsLayout({
  addLayout: () => undefined,
});
void layoutConfig.type;

const meter: obs.Metrics = obs.metrics.init({ community: 'openeuler' });
const counter: obs.Counter = meter.counter('demo_total', 'demo counter', ['kind']);
counter.inc(1, { kind: 'a' });
const gauge: obs.Gauge = meter.gauge('demo_in_flight', 'demo gauge');
gauge.set(3);
gauge.inc(1);
const histogram: obs.Histogram = meter.histogram('demo_duration_seconds', 'demo latency');
histogram.observe(0.2);
void meter.text().then((body: string) => body.length);
const community: string = meter.defaultCommunity;
void community;

obs.context.bindRequest({ community: 'mindspore', requestId: 'req-1' }, () => {
  const current: obs.ContextStore = obs.context.current();
  void current;
  const requestId: string | null = obs.context.requestId();
  void requestId;
  const traceId: string | null = obs.context.traceId();
  void traceId;
  const spanId: string | null = obs.context.spanId();
  void spanId;
  const scopedCommunity: string | null = obs.context.community();
  void scopedCommunity;
});

const middleware = obs.middleware.makeMiddleware({
  metrics: meter,
  resolveCommunity: (req: any) => (req.url === '/mindspore' ? 'mindspore' : undefined),
  collectServerMetrics: false,
});
middleware({ headers: {}, url: '/' }, {}, () => undefined);

const headerName: string = obs.middleware.HEADER_REQUEST_ID;
void headerName;
const buckets: number[] = obs.middleware.DEFAULT_METRIC_BUCKETS;
void buckets;
void obs.middleware.metricsRouteHandler(meter);
