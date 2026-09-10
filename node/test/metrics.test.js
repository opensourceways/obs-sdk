'use strict';

const { test } = require('node:test');
const assert = require('node:assert');
const { Metrics } = require('../lib/metrics');
const context = require('../lib/context');

test('counter 带公共 label + community 双层注入', async () => {
  const m = new Metrics({ service: 'review', env: 'test', instance: 'pod-1',
    community: 'openeuler' });
  const c = m.counter('events_total', 'events', ['kind']);
  c.inc(1, { kind: 'pr' });
  context.bindRequest({ community: 'mindspore' }, () => {
    c.inc(1, { kind: 'pr' });
  });

  const text = await m.text();
  const rows = text.split('\n').filter((l) => /^events_total\{/.test(l));
  assert.strictEqual(rows.length, 2);
  const byComm = {};
  for (const r of rows) {
    const matched = r.match(/community="([^"]+)"/);
    byComm[matched[1]] = r;
  }
  assert.ok(byComm.openeuler.endsWith(' 1'));
  assert.ok(byComm.mindspore.endsWith(' 1'));
  assert.ok(byComm.openeuler.includes('service="review"'));
});

test('gauge + histogram', async () => {
  const m = new Metrics({ service: 'review', community: 'openeuler' });
  const g = m.gauge('in_flight', 'in flight');
  g.set(3);
  g.inc(2);
  const h = m.histogram('latency_seconds', 'latency');
  h.observe(0.1);
  h.observe(0.2);

  const text = await m.text();
  const gaugeRows = text.split('\n').filter((l) => /^in_flight\{/.test(l));
  assert.ok(gaugeRows[0].endsWith(' 5'));
  assert.ok(text.includes('latency_seconds_count'));
  const countRows = text.split('\n').filter((l) => /^latency_seconds_count\{/.test(l));
  assert.ok(countRows[0].endsWith(' 2'));
});

test('namespace 前缀', async () => {
  const m = new Metrics({ service: 'review', namespace: 'obs' });
  m.counter('events_total', 'events').inc(1);
  const text = await m.text();
  assert.ok(text.includes('obs_events_total'));
});
