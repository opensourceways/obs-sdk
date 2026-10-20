'use strict';

// 结构化 JSON 日志（obs-sdk-node 的 log 部分）。单行 JSON 写 stream（默认 stdout），
// 字段规范见 spec/log-format.md；请求级字段来自 ./context。
//
// 固定字段顺序：time / level / msg / service / env / instance / community /
// [request_id] / [trace_id] / [span_id] / [logger] / [error] / 业务字段。
// logger 为调用位置（末两级路径:行号），对应 Go SDK 的 record.PC 与 Java 的
// File.java:line；error 为异常堆栈。业务字段只能追加在固定字段之后。

const util = require('node:util');
const env = require('./env');
const context = require('./context');

const LEVELS = { debug: 10, info: 20, warn: 30, error: 40 };

// 固定字段不允许被业务字段覆盖；error 单独处理，因此不在跳过集合里。
const RESERVED = new Set([
  'time', 'level', 'msg', 'service', 'env', 'instance', 'community',
  'request_id', 'trace_id', 'span_id', 'logger',
]);

const defaults = {
  service: 'unknown',
  env: 'unknown',
  instance: 'unknown',
  community: 'unknown',
};
let minLevel = LEVELS.info;
let stream = process.stdout;
let disableSource = false;

// init({service, env, instance, community, level, stream, disableSource})：
// 进程启动时装配。disableSource 对应 Go SDK 的 DisableSource，用于高吞吐场景
// 关闭调用位置采集。
function init(opts = {}) {
  defaults.service = env.service(opts.service);
  defaults.env = env.env(opts.env);
  defaults.instance = env.instance(opts.instance);
  defaults.community = env.community(opts.community);
  minLevel = LEVELS[opts.level] !== undefined ? LEVELS[opts.level] : LEVELS.info;
  if (opts.stream) stream = opts.stream;
  disableSource = opts.disableSource === true;
  return api;
}

function formatError(err) {
  if (err && typeof err.stack === 'string' && err.stack) return err.stack;
  if (err && typeof err.message === 'string') return err.message;
  return String(err);
}

// 业务值统一压成标量：异常取堆栈；对象/数组序列化为字符串，保证顶层不出现
// 嵌套结构（spec/log-format.md「禁止嵌套对象」）。
function normalize(value) {
  if (value instanceof Error) return formatError(value);
  if (typeof value === 'bigint') return value.toString();
  if (value === undefined) return undefined;
  if (value === null) return null;
  if (typeof value === 'object') {
    try {
      return JSON.stringify(value);
    } catch (_) {
      return String(value);
    }
  }
  return value;
}

// 从调用栈里取首个业务帧，输出末两级路径 + 行号，例如 express/specialpages.ts:405。
// SDK 自身与 log4js 的帧会被跳过。
function parseLocation(stack) {
  if (!stack) return undefined;
  for (const raw of String(stack).split('\n')) {
    const m = raw.match(/(?:\()?(?:file:\/\/)?([^()\s]+):(\d+):\d+\)?$/);
    if (!m) continue;
    const file = m[1];
    if (file.startsWith('node:')) continue;
    if (file.includes('node:internal')) continue;
    if (file.includes('/log4js/')) continue;
    if (/[\\/]lib[\\/]log\.js$/.test(file)) continue;
    const parts = file.split(/[\\/]/);
    const base = parts.pop();
    const dir = parts.pop();
    return dir ? `${dir}/${base}:${m[2]}` : `${base}:${m[2]}`;
  }
  return undefined;
}

function buildRecord(levelName, msg, fields, opts = {}) {
  const req = context.current();
  const record = {
    time: (opts.time instanceof Date ? opts.time : new Date()).toISOString(),
    level: levelName,
    msg: typeof msg === 'string' ? msg : String(msg === undefined ? '' : msg),
    service: defaults.service,
    env: defaults.env,
    instance: defaults.instance,
    community: req.community || defaults.community,
  };
  if (req.requestId) record.request_id = req.requestId;
  if (req.traceId) record.trace_id = req.traceId;
  if (req.spanId) record.span_id = req.spanId;
  if (opts.logger) record.logger = opts.logger;
  if (fields && fields.error !== undefined) {
    record.error = normalize(fields.error);
  }
  if (fields) {
    for (const key of Object.keys(fields)) {
      if (key === 'error' || RESERVED.has(key)) continue;
      const value = normalize(fields[key]);
      if (value !== undefined) record[key] = value;
    }
  }
  return record;
}

function log(levelName, msg, fields) {
  const lv = LEVELS[levelName];
  if (lv === undefined || lv < minLevel) return;

  const logger = disableSource ? undefined : parseLocation(new Error().stack);
  const line = JSON.stringify(buildRecord(levelName, msg, fields, { logger }));
  stream.write(line + '\n');
}

function debug(msg, fields) { log('debug', msg, fields); }
function info(msg, fields) { log('info', msg, fields); }
function warn(msg, fields) { log('warn', msg, fields); }
function error(msg, fields) { log('error', msg, fields); }

function isPlainObject(value) {
  if (value === null || typeof value !== 'object') return false;
  if (value instanceof Error || Array.isArray(value) || value instanceof Date) return false;
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

function mapLevel(level) {
  let name = '';
  if (typeof level === 'string') name = level;
  else if (level && typeof level.levelStr === 'string') name = level.levelStr;
  name = name.toLowerCase();
  if (name === 'trace') return 'debug';
  if (name === 'fatal') return 'error';
  return LEVELS[name] !== undefined ? name : 'info';
}

// makeLog4jsLayout({disableSource})：log4js 自定义 layout 工厂，不做框架级依赖。
// 约定 event.data[0] 为 msg，其余 plain object 平铺为业务字段，Error 进 error；
// 其余参数交由 util.format 兼容存量 printf 风格日志。调用位置取自
// event.callStack，因此需要在 log4js 的 category 配置里开启 enableCallStack: true。
function makeLog4jsLayout(opts = {}) {
  const withSource = opts.disableSource === undefined
    ? !disableSource
    : opts.disableSource !== true;
  return function layout(event) {
    const data = Array.isArray(event.data) ? event.data : [event.data];
    let msg = '';
    const fields = {};
    let error;

    if (typeof data[0] === 'string') {
      msg = data[0];
      const legacy = [];
      for (const item of data.slice(1)) {
        if (item instanceof Error) error = item;
        else if (isPlainObject(item)) Object.assign(fields, item);
        else legacy.push(item);
      }
      if (legacy.length) msg = util.format(msg, ...legacy);
    } else {
      msg = util.format(...data);
    }
    if (error) fields.error = error;

    const logger = withSource ? parseLocation(event.callStack) : undefined;
    return JSON.stringify(buildRecord(mapLevel(event.level), msg, fields, {
      time: event.startTime instanceof Date ? event.startTime : undefined,
      logger,
    }));
  };
}

// registerLog4jsLayout(log4js, {disableSource})：把 obs layout 注册为 log4js 的
// 'obs' 类型，返回可直接放进 appender.layout 的配置。log4js 由调用方传入，
// SDK 不新增框架依赖。
function registerLog4jsLayout(log4js, opts = {}) {
  if (!log4js || typeof log4js.addLayout !== 'function') {
    throw new TypeError('registerLog4jsLayout requires the log4js module');
  }
  log4js.addLayout('obs', () => makeLog4jsLayout(opts));
  return { type: 'obs' };
}

const api = { init, debug, info, warn, error, makeLog4jsLayout, registerLog4jsLayout };
module.exports = api;
