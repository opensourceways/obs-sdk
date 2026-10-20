// obs-sdk-node 类型声明，与 index.js / lib/*.js 的实现保持同步。
// 使用方 `import obs from 'obs-sdk-node'`（需 esModuleInterop）或
// `import obs = require('obs-sdk-node')`；类型可通过命名空间引用，如 `obs.Fields`。

declare namespace obs {
  /** 业务日志字段；值会被压平为标量（Error 取堆栈，对象/数组序列化为字符串）。 */
  type Fields = Record<string, unknown>;

  type Level = 'debug' | 'info' | 'warn' | 'error';

  interface WritableStream {
    write(chunk: string): unknown;
  }

  interface LogInitOptions {
    service?: string;
    env?: string;
    instance?: string;
    community?: string;
    level?: Level;
    stream?: WritableStream;
    disableSource?: boolean;
  }

  interface LayoutOptions {
    disableSource?: boolean;
  }

  interface Log4jsLogEvent {
    startTime?: Date;
    data?: unknown;
    callStack?: string;
    level?: string;
    [key: string]: unknown;
  }

  interface Log4jsLike {
    addLayout(name: string, factory: () => (event: Log4jsLogEvent) => string): void;
  }

  interface Log4jsLayoutConfig {
    type: string;
  }

  interface Log {
    init(options?: LogInitOptions): Log;
    debug(msg: string, fields?: Fields): void;
    info(msg: string, fields?: Fields): void;
    warn(msg: string, fields?: Fields): void;
    error(msg: string, fields?: Fields): void;
    makeLog4jsLayout(options?: LayoutOptions): (event: Log4jsLogEvent) => string;
    registerLog4jsLayout(log4js: Log4jsLike, options?: LayoutOptions): Log4jsLayoutConfig;
  }

  interface MetricLabels {
    [label: string]: string | number;
  }

  interface MetricsOptions {
    service?: string;
    env?: string;
    instance?: string;
    community?: string;
    namespace?: string;
  }

  interface Counter {
    inc(amount?: number, labels?: MetricLabels): void;
  }

  interface Gauge {
    set(value: number, labels?: MetricLabels): void;
    inc(amount?: number, labels?: MetricLabels): void;
  }

  interface Histogram {
    observe(value: number, labels?: MetricLabels): void;
  }

  interface Metrics {
    readonly registry: unknown;
    readonly defaultCommunity: string;
    counter(name: string, help: string, labelNames?: string[]): Counter;
    gauge(name: string, help: string, labelNames?: string[]): Gauge;
    histogram(name: string, help: string, labelNames?: string[]): Histogram;
    text(): Promise<string>;
  }

  interface MetricsConstructor {
    new (options?: MetricsOptions): Metrics;
  }

  interface MetricsModule {
    Metrics: MetricsConstructor;
    init(options?: MetricsOptions): Metrics;
    defaultMetrics(): Metrics;
  }

  interface ContextStore {
    community?: string;
    requestId?: string;
    traceId?: string;
    spanId?: string;
  }

  interface ContextModule {
    current(): ContextStore;
    community(): string | null;
    requestId(): string | null;
    traceId(): string | null;
    spanId(): string | null;
    bindRequest<T>(fields: ContextStore, fn: () => T): T;
  }

  type NextFunction = (err?: unknown) => void;

  interface MiddlewareOptions {
    metrics?: Metrics;
    resolveCommunity?: (req: any) => string | undefined;
    collectServerMetrics?: boolean;
  }

  interface MetricsRouteResult {
    statusCode: number;
    contentType: string;
    body: string;
  }

  interface MiddlewareModule {
    readonly HEADER_REQUEST_ID: string;
    readonly DEFAULT_METRIC_BUCKETS: number[];
    Metrics: MetricsConstructor;
    makeMiddleware(options?: MiddlewareOptions): (req: any, res: any, next: NextFunction) => void;
    metricsRouteHandler(metrics: Metrics): Promise<MetricsRouteResult>;
  }

  const log: Log;
  const metrics: MetricsModule;
  const context: ContextModule;
  const middleware: MiddlewareModule;
}

export = obs;
