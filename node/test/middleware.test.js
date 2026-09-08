'use strict';

const { test } = require('node:test');
const assert = require('node:assert');
const { Metrics } = require('../lib/metrics');
const context = require('../lib/context');
const { makeMiddleware } = require('../lib/middleware');

// 极简 req/res 模拟。handler 为中间件的下游（在中间件 bind 的作用域内执行），
// 结束后触发 finish（中间件靠它记账）再 resolve。
function run(mw, { method = 'GET', url = '/jobs', headers = {}, status = 200 } = {}, handler) {
  return new Promise((resolve) => {
    const req = { method, url, originalUrl: url, headers };
    const listeners = {};
    const res = {
      statusCode: status,
      on(ev, cb) { (listeners[ev] = listeners[ev] || []).push(cb); return this; },
    };
    mw(req, res, () => {
      res.statusCode = status;
      if (handler) handler();
      for (const cb of listeners.finish || []) cb();
      resolve();
    });
  });
}

test('注入 request_id（沿用入站头）', async () => {
  let got = null;
  const mw = makeMiddleware({});
  await run(mw, { headers: { 'x-request-id': 'inbound-1' } }, () => {
    got = context.requestId();
  });
  assert.strictEqual(got, 'inbound-1');
});

test('无入站头则生成 request_id 且允许 community resolver', async () => {
  let got = null;
  const mw = makeMiddleware({ resolveCommunity: () => 'openeuler' });
  await run(mw, {}, () => {
    got = { rid: context.requestId(), comm: context.community() };
  });
  assert.ok(got.rid);
  assert.strictEqual(got.comm, 'openeuler');
});

test('服务器指标带公共 label 与 community', async () => {
  const m = new Metrics({ service: 'review', env: 'test', instance: 'pod-1',
    community: 'openeuler' });
  const mw = makeMiddleware({ metrics: m, resolveCommunity: () => 'openeuler' });

  await run(mw, { method: 'POST', url: '/jobs', status: 201 });

  const text = await m.text();
  const rows = text.split('\n').filter((l) => /^http_server_requests_total\{/.test(l));
  assert.strictEqual(rows.length, 1);
  assert.ok(rows[0].includes('service="review"'));
  assert.ok(rows[0].includes('community="openeuler"'));
  assert.ok(rows[0].includes('method="POST"'));
  assert.ok(rows[0].includes('path="/jobs"'));
  assert.ok(rows[0].includes('status_code="201"'));
  assert.ok(rows[0].endsWith(' 1'));
});

test('community 双层注入：resolver 覆盖 > 部署默认', async () => {
  const m = new Metrics({ service: 'review', community: 'openeuler' });
  // resolver 按 path 判定（演示可信判定点）；/mindspore 走覆盖，其余默认。
  const mw = makeMiddleware({
    metrics: m,
    resolveCommunity: (req) => (req.url.startsWith('/mindspore') ? 'mindspore' : undefined),
  });

  await run(mw, { url: '/jobs' });
  await run(mw, { url: '/mindspore/jobs' });

  const text = await m.text();
  const rows = text.split('\n').filter((l) => /^http_server_requests_total\{/.test(l));
  const byComm = {};
  for (const r of rows) byComm[r.match(/community="([^"]+)"/)[1]] = r;
  assert.ok(byComm.openeuler);  // 无覆盖 path → 部署默认
  assert.ok(byComm.mindspore);  // /mindspore → 覆盖
});
