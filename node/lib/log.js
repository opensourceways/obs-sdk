'use strict';

// 结构化 JSON 日志（obs-sdk-node 的 log 部分）。单行 JSON 写 stdout，
// 字段规范见 spec/log-format.md；请求级字段来自 ./context。

const env = require('./env');
const context = require('./context');

const LEVELS = { debug: 10, info: 20, warn: 30, error: 40 };

const defaults = {
  service: 'unknown',
  env: 'unknown',
  instance: 'unknown',
  community: 'unknown',
};
let minLevel = LEVELS.info;
let stream = process.stdout;

// init({service, env, instance, community, level, stream})：进程启动时装配。
function init(opts = {}) {
  defaults.service = env.service(opts.service);
  defaults.env = env.env(opts.env);
  defaults.instance = env.instance(opts.instance);
  defaults.community = env.community(opts.community);
  minLevel = LEVELS[opts.level] !== undefined ? LEVELS[opts.level] : LEVELS.info;
  if (opts.stream) stream = opts.stream;
  return api;
}

function log(levelName, msg, fields) {
  const lv = LEVELS[levelName];
  if (lv < minLevel) return;

  const req = context.current();
  const record = Object.assign({}, fields || {}, {
    service: defaults.service,
    env: defaults.env,
    instance: defaults.instance,
    community: req.community || defaults.community,
    level: levelName,
    msg,
    time: new Date().toISOString(),
  });
  if (req.requestId) record.request_id = req.requestId;
  if (req.traceId) record.trace_id = req.traceId;

  const line = JSON.stringify(record);
  stream.write(line + '\n');
}

function debug(msg, fields) { log('debug', msg, fields); }
function info(msg, fields) { log('info', msg, fields); }
function warn(msg, fields) { log('warn', msg, fields); }
function error(msg, fields) { log('error', msg, fields); }

const api = { init, debug, info, warn, error };
module.exports = api;
