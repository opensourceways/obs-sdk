'use strict';

// 请求级通用字段（Node 版 sdkctx）。用 AsyncLocalStorage 实现线程/并发隔离，
// 语义同 Python contextvars（见 spec/common-fields.md）。
//
// 框架适配器在入口用 bindRequest 包裹请求处理，SDK log/metrics 读取当前
// 上下文；未绑定则回退部署级默认。

const { AsyncLocalStorage } = require('async_hooks');

const storage = new AsyncLocalStorage();

function current() {
  return storage.getStore() || {};
}

function community() {
  return current().community || null;
}

function requestId() {
  return current().requestId || null;
}

function traceId() {
  return current().traceId || null;
}

// bindRequest(store, fn)：在 store（{community?, requestId?, traceId?}）内执行 fn。
// 会与已有上下文合并（缺省字段继承外层）。
function bindRequest(fields, fn) {
  const prev = storage.getStore() || {};
  const merged = {
    community: fields.community !== undefined ? fields.community : prev.community,
    requestId: fields.requestId !== undefined ? fields.requestId : prev.requestId,
    traceId: fields.traceId !== undefined ? fields.traceId : prev.traceId,
  };
  return storage.run(merged, fn);
}

module.exports = { current, community, requestId, traceId, bindRequest };
