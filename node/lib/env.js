'use strict';

// OBS_* 环境变量解析（与其他语言 SDK 一致，见 spec/common-fields.md）。

const os = require('os');

function resolve(explicit, envName, fallback) {
  if (explicit) return explicit;
  const v = process.env[envName];
  return v || fallback;
}

function service(explicit) { return resolve(explicit, 'OBS_SERVICE', 'unknown'); }
function env(explicit) { return resolve(explicit, 'OBS_ENV', 'unknown'); }
function instance(explicit) {
  if (explicit) return explicit;
  return process.env.OBS_INSTANCE || os.hostname() || 'unknown';
}
function community(explicit) { return resolve(explicit, 'OBS_COMMUNITY', 'unknown'); }

module.exports = { service, env, instance, community };
