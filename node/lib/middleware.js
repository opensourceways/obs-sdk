'use strict';

// Express/通用中间件（obs-sdk-node）。
//
// 职责（薄装配，见 spec/common-fields.md）：注入 request_id（沿用可信入站头
// X-Request-Id 或生成）；可选从可信判定点解析 community；可选记录服务器指标
// obs_http_server_requests_total / obs_http_server_request_duration_seconds。

const { randomUUID } = require('crypto');
const context = require('./context');
const { Metrics } = require('./metrics');

const HEADER_REQUEST_ID = 'X-Request-Id';
const DEFAULT_METRIC_BUCKETS = [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10];

// makeMiddleware({metrics, resolveCommunity, collectServerMetrics}) → express middleware。
function makeMiddleware({ metrics, resolveCommunity, collectServerMetrics = true } = {}) {
  let counterTotal = null;
  let duration = null;
  if (metrics && collectServerMetrics) {
    counterTotal = metrics.counter('http_server_requests_total', 'HTTP requests handled', ['method', 'path', 'status_code']);
    duration = metrics.histogram('http_server_request_duration_seconds', 'HTTP request latency', ['method', 'path', 'status_code']);
  }

  return function obsMiddleware(req, res, next) {
    const community = resolveCommunity ? resolveCommunity(req) : undefined;
    const inboundRid = req.headers[HEADER_REQUEST_ID.toLowerCase()];
    const fields = {
      community,
      requestId: inboundRid || randomUUID(),
    };

    context.bindRequest(fields, () => {
      const startHr = process.hrtime();
      res.on('finish', () => {
        if (counterTotal) {
          const durSec = process.hrtime(startHr)[0] + process.hrtime(startHr)[1] / 1e9;
          const labels = {
            method: req.method,
            path: req.originalUrl ? req.originalUrl.split('?')[0] : req.url,
            status_code: String(res.statusCode),
          };
          counterTotal.inc(1, labels);
          duration.observe(durSec, labels);
        }
      });
      next();
    });
  };
}

// metricsRouteHandler(metrics)：给 express 挂 /metrics 用。
async function metricsRouteHandler(metrics) {
  const body = await metrics.text();
  return {
    statusCode: 200,
    contentType: 'text/plain; version=0.0.4; charset=utf-8',
    body,
  };
}

module.exports = { makeMiddleware, metricsRouteHandler, HEADER_REQUEST_ID, DEFAULT_METRIC_BUCKETS, Metrics };
