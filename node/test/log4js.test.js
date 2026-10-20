'use strict';

const { test } = require('node:test');
const assert = require('node:assert');
const log = require('../lib/log');
const context = require('../lib/context');

function ev(data, { level = 'INFO', callStack, startTime } = {}) {
  return { data, level: { levelStr: level }, callStack, startTime };
}

const CALL_STACK = 'Error\n'
  + '    at handler (/app/src/node/hooks/express/specialpages.ts:405:12)\n'
  + '    at Layer.handle (/app/node_modules/express/lib/router/layer.js:95:5)\n';

test('log4js layout：常量 msg + kv 平铺 + 调用位置', () => {
  log.init({ service: 'etherpad-lite', env: 'prod', instance: 'pod-1',
    community: 'openeuler' });
  const layout = log.makeLog4jsLayout();

  const line = layout(ev(['open pad', { pad_id: 'p1', event: 'pad_open' }], {
    callStack: CALL_STACK,
    startTime: new Date('2026-10-20T07:12:34.567Z'),
  }));

  assert.ok(!line.includes('\n'));
  const rec = JSON.parse(line);
  assert.strictEqual(rec.time, '2026-10-20T07:12:34.567Z');
  assert.strictEqual(rec.level, 'info');
  assert.strictEqual(rec.msg, 'open pad');
  assert.strictEqual(rec.service, 'etherpad-lite');
  assert.strictEqual(rec.env, 'prod');
  assert.strictEqual(rec.instance, 'pod-1');
  assert.strictEqual(rec.community, 'openeuler');
  assert.strictEqual(rec.pad_id, 'p1');
  assert.strictEqual(rec.event, 'pad_open');
  assert.strictEqual(rec.logger, 'express/specialpages.ts:405');
});

test('log4js layout：TRACE/FATAL 映射到契约 level', () => {
  const layout = log.makeLog4jsLayout();
  assert.strictEqual(JSON.parse(layout(ev(['x'], { level: 'TRACE' }))).level, 'debug');
  assert.strictEqual(JSON.parse(layout(ev(['x'], { level: 'FATAL' }))).level, 'error');
});

test('log4js layout：Error 进 error 字段且 msg 不被污染', () => {
  const layout = log.makeLog4jsLayout();
  const rec = JSON.parse(layout(ev(['handle api call failed',
    { api_func: 'getPad' }, new Error('boom')])));

  assert.strictEqual(rec.msg, 'handle api call failed');
  assert.strictEqual(rec.api_func, 'getPad');
  assert.match(rec.error, /^Error: boom/);
  // 未开启 enableCallStack 时不输出 logger。
  assert.ok(!('logger' in rec));
});

test('log4js layout：兼容 printf 风格存量日志', () => {
  const layout = log.makeLog4jsLayout();
  const rec = JSON.parse(layout(ev(['pad %s opened by %s', 'p1', 'alice'])));
  assert.strictEqual(rec.msg, 'pad p1 opened by alice');
});

test('log4js layout：保留键不可被业务字段覆盖 + 请求上下文', () => {
  const layout = log.makeLog4jsLayout();
  context.bindRequest({ requestId: 'req-1', community: 'mindspore' }, () => {
    const rec = JSON.parse(layout(ev(['scoped', { service: 'evil', custom: 'v' }])));
    assert.strictEqual(rec.service, 'etherpad-lite');
    assert.strictEqual(rec.community, 'mindspore');
    assert.strictEqual(rec.request_id, 'req-1');
    assert.strictEqual(rec.custom, 'v');
  });
});

test('log4js layout：disableSource 关闭调用位置', () => {
  log.init({ service: 'etherpad-lite', community: 'openeuler' });
  const layout = log.makeLog4jsLayout({ disableSource: true });
  const rec = JSON.parse(layout(ev(['x'], { callStack: CALL_STACK })));
  assert.ok(!('logger' in rec));
});

test('registerLog4jsLayout 注册 obs layout 并返回 appender 配置', () => {
  log.init({ service: 'etherpad-lite', community: 'openeuler' });
  const registered = {};
  const fakeLog4js = {
    addLayout(name, generator) { registered[name] = generator; },
  };

  const cfg = log.registerLog4jsLayout(fakeLog4js, { disableSource: true });
  assert.deepStrictEqual(cfg, { type: 'obs' });

  const rec = JSON.parse(registered.obs()(ev(['x'], { callStack: CALL_STACK })));
  assert.strictEqual(rec.service, 'etherpad-lite');
  assert.ok(!('logger' in rec));
});
