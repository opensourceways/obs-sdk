'use strict';

const { test } = require('node:test');
const assert = require('node:assert');
const { Writable } = require('node:stream');
const log = require('../lib/log');
const context = require('../lib/context');

function capture() {
  const chunks = [];
  const stream = new Writable({
    write(c, _e, cb) { chunks.push(c.toString()); cb(); },
  });
  return { stream, lines: () => chunks.map(JSON.parse) };
}

test('静态字段注入 + community 双层注入', () => {
  const { stream, lines } = capture();
  log.init({ service: 'review', env: 'test', instance: 'pod-1',
    community: 'openeuler', level: 'info', stream });

  log.info('hello', { event: 'pr' });
  context.bindRequest({ community: 'mindspore', requestId: 'req-1' }, () => {
    log.info('scoped');
  });

  const out = lines();
  assert.strictEqual(out.length, 2);

  const a = out[0];
  assert.strictEqual(a.service, 'review');
  assert.strictEqual(a.env, 'test');
  assert.strictEqual(a.instance, 'pod-1');
  assert.strictEqual(a.community, 'openeuler');
  assert.strictEqual(a.level, 'info');
  assert.strictEqual(a.msg, 'hello');
  assert.strictEqual(a.event, 'pr');
  // 无请求上下文时预留字段不出现。
  assert.ok(!('request_id' in a));
  assert.ok(!('trace_id' in a));
  assert.ok(!('span_id' in a));

  const b = out[1];
  assert.strictEqual(b.community, 'mindspore');
  assert.strictEqual(b.request_id, 'req-1');
});

test('level 过滤 + trace_id 预留注入', () => {
  const { stream, lines } = capture();
  log.init({ service: 'srv', community: 'openeuler', level: 'warn', stream });

  log.info('dropped');
  log.warn('kept');
  context.bindRequest({ traceId: 'trace-xyz' }, () => {
    log.warn('with trace');
  });

  const out = lines();
  assert.strictEqual(out.length, 2);
  assert.strictEqual(out[0].msg, 'kept');
  assert.strictEqual(out[1].trace_id, 'trace-xyz');
  assert.strictEqual(out[1].community, 'openeuler');
  // 未 bind spanId 时不输出该键。
  assert.ok(!('span_id' in out[1]));
});

test('固定字段顺序 + logger + error + 业务字段规范化', () => {
  const { stream, lines } = capture();
  log.init({ service: 'srv', env: 'test', instance: 'pod-1',
    community: 'openeuler', stream });

  log.error('boom', { error: new Error('kaboom'), custom: 'v', nested: { a: 1 } });

  const [rec] = lines();
  assert.deepStrictEqual(Object.keys(rec), [
    'time', 'level', 'msg', 'service', 'env', 'instance', 'community',
    'logger', 'error', 'custom', 'nested',
  ]);
  assert.strictEqual(rec.msg, 'boom');
  assert.match(rec.logger, /^test\/log\.test\.js:\d+$/);
  assert.match(rec.error, /^Error: kaboom/);
  assert.strictEqual(rec.custom, 'v');
  // 嵌套对象压成字符串，保证顶层不出现嵌套结构。
  assert.strictEqual(rec.nested, '{"a":1}');
});

test('保留键不可被业务字段覆盖', () => {
  const { stream, lines } = capture();
  log.init({ service: 'srv', community: 'openeuler', stream });

  log.info('keep', {
    service: 'evil', msg: 'evil', level: 'evil', logger: 'evil',
    request_id: 'evil', time: 'evil', custom: 'v',
  });

  const [rec] = lines();
  assert.strictEqual(rec.service, 'srv');
  assert.strictEqual(rec.msg, 'keep');
  assert.strictEqual(rec.level, 'info');
  assert.notStrictEqual(rec.logger, 'evil');
  assert.ok(!('request_id' in rec));
  assert.notStrictEqual(rec.time, 'evil');
  assert.strictEqual(rec.custom, 'v');
});

test('disableSource 关闭调用位置；BigInt/undefined 规范化', () => {
  const { stream, lines } = capture();
  log.init({ service: 'srv', community: 'openeuler', stream, disableSource: true });

  log.info('no source', { n: 10n, skip: undefined });

  const [rec] = lines();
  assert.ok(!('logger' in rec));
  assert.strictEqual(rec.n, '10');
  assert.ok(!('skip' in rec));
});

test('span_id 预留注入（与 trace_id 互不干扰）', () => {
  const { stream, lines } = capture();
  log.init({ service: 'srv', community: 'openeuler', stream });

  context.bindRequest({ traceId: 'trace-xyz', spanId: 'span-abc' }, () => {
    log.info('with span');
  });
  context.bindRequest({ spanId: 'span-only' }, () => {
    log.info('span only');
  });

  const out = lines();
  assert.strictEqual(out[0].span_id, 'span-abc');
  assert.strictEqual(out[0].trace_id, 'trace-xyz');
  assert.strictEqual(out[1].span_id, 'span-only');
  assert.ok(!('trace_id' in out[1]));
});
