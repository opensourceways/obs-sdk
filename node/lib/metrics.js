'use strict';

// Prometheus 指标装配（obs-sdk-node 的 metrics 部分）。底层用官方库 prom-client，
// 不自研 instrumentation，只做装配与 label 对齐（见 spec/metrics-format.md）：
//   - 每个指标带 service/env/instance/community label；
//   - community 是唯一可动态项（ctx 覆盖优先，否则部署默认，双层注入）；
//   - Metrics.text() 输出 Prometheus text 供 /metrics。
//
// prom-client 的 labelNames 声明后值必须全给，故返回薄 wrapper 自动填公共维度，
// 业务只传业务 label。

const { Registry, Counter, Gauge, Histogram } = require('prom-client');
const env = require('./env');
const context = require('./context');

const COMMON = ['service', 'env', 'instance', 'community'];

class Metrics {
  constructor({ service, env: envName, instance, community, namespace } = {}) {
    this._registry = new Registry();
    this._service = env.service(service);
    this._env = env.env(envName);
    this._instance = env.instance(instance);
    this._defaultCommunity = env.community(community);
    this._namespace = namespace || '';
    // 不含 process 默认采集器；业务若需要 process/内存指标自行 registry.collectDefaultMetrics。
  }

  get registry() { return this._registry; }
  get defaultCommunity() { return this._defaultCommunity; }

  _fq(name) { return this._namespace ? `${this._namespace}_${name}` : name; }

  _commonVals(extra) {
    const c = context.community() || this._defaultCommunity;
    return Object.assign({ service: this._service, env: this._env,
      instance: this._instance, community: c }, extra);
  }

  // counter(name, help, labelNames?) → { inc(amount, labels) }
  counter(name, help, labelNames = []) {
    const metric = new Counter({
      name: this._fq(name), help,
      labelNames: [...COMMON, ...labelNames],
      registers: [this._registry],
    });
    return {
      inc: (amount = 1, labels = {}) => metric.inc(this._commonVals(labels), amount),
    };
  }

  gauge(name, help, labelNames = []) {
    const metric = new Gauge({
      name: this._fq(name), help,
      labelNames: [...COMMON, ...labelNames],
      registers: [this._registry],
    });
    return {
      set: (value, labels = {}) => metric.set(this._commonVals(labels), value),
      inc: (amount = 1, labels = {}) => metric.inc(this._commonVals(labels), amount),
    };
  }

  histogram(name, help, labelNames = []) {
    const metric = new Histogram({
      name: this._fq(name), help,
      labelNames: [...COMMON, ...labelNames],
      registers: [this._registry],
    });
    return {
      observe: (value, labels = {}) => metric.observe(this._commonVals(labels), value),
    };
  }

  async text() {
    return this._registry.metrics();
  }
}

// 便捷单例（默认读 OBS_* 环境变量）。
let _default = null;
function init(opts = {}) {
  if (!_default) _default = new Metrics(opts);
  return _default;
}
function defaultMetrics() {
  if (!_default) return init();
  return _default;
}

module.exports = { Metrics, init, defaultMetrics };
