"""OBS_* 环境变量解析辅助（与 go/internal/env 语义一致，见 spec/common-fields.md）。

四语言 SDK 读同一套环境变量：OBS_SERVICE / OBS_ENV / OBS_INSTANCE / OBS_COMMUNITY。
解析优先级：显式参数 > 环境变量 > 内置默认。
"""

from __future__ import annotations

import os
import socket


def _resolve(explicit: str | None, env_name: str, default: str) -> str:
    if explicit:
        return explicit
    value = os.getenv(env_name)
    if value:
        return value
    return default


def service(explicit: str | None = None) -> str:
    return _resolve(explicit, "OBS_SERVICE", "unknown")


def env(explicit: str | None = None) -> str:
    return _resolve(explicit, "OBS_ENV", "unknown")


def instance(explicit: str | None = None) -> str:
    if explicit:
        return explicit
    value = os.getenv("OBS_INSTANCE")
    if value:
        return value
    return socket.gethostname() or "unknown"


def community(explicit: str | None = None) -> str:
    return _resolve(explicit, "OBS_COMMUNITY", "unknown")
