"""结构化 JSON 日志（obs-sdk-python 的 log 部分）。

格式遵循 spec/log-format.md：
  - 单行 JSON，经 stdout 进 LTS；
  - 常驻字段 service/env/instance/community 在 init 时注入；
  - 请求级 community 覆盖 / request_id / trace_id / span_id 从 _context 读取；
  - trace_id / span_id 预留位（有值才输出，二期经 _context.bind(trace_id=..., span_id=...) 注入）。

用法：

    from obs_sdk import log
    log.init(service="meeting-center")
    logger = log.get_logger(__name__)
    logger.info("hello", extra={"event": "xxx"})

请求级覆盖（框架适配器已在入口 bind）：

    with _context.bind(community="openeuler", request_id="req-1"):
        logger.info("scoped")
"""

from __future__ import annotations

import json
import logging
import sys
import time
from typing import Any, Optional

from . import _context, _env

# 标准字段，不当作业务字段输出。
_RESERVED = {
    "name", "msg", "args", "levelname", "levelno", "pathname", "filename",
    "module", "exc_info", "exc_text", "stack_info", "lineno", "funcName",
    "created", "msecs", "relativeCreated", "thread", "threadName",
    "processName", "process", "taskName", "message",
}

# 标识本 SDK 挂在 logger 上的 handler。
_SDK_HANDLER_NAME = "obs-sdk-json"

# SDK 自己的 logger 名。log.info(...) 这类便捷函数走它，而不是 root —— root 是
# 宿主应用的全局开关，SDK 不去改它的 level（见 get_logger 注释）。
_SDK_LOGGER_NAME = "obs_sdk"


class JsonFormatter(logging.Formatter):
    """把日志记录格式化为单行 JSON。"""

    def __init__(self, *, service: str, env: str, instance: str,
                 community: str) -> None:
        super().__init__()
        self._service = service
        self._env = env
        self._instance = instance
        self._default_community = community

    def format(self, record: logging.LogRecord) -> str:
        # 先收业务 extra（不得覆盖常驻字段，见下）。
        fields: dict[str, Any] = {}
        for key, value in record.__dict__.items():
            if key in _RESERVED or not isinstance(key, str) or key.startswith("_"):
                continue
            fields[key] = _serialize(value)

        # 请求级字段优先于静态默认。
        req = _context.get()
        community = req.community or self._default_community
        if req.request_id:
            fields["request_id"] = req.request_id
        if req.trace_id:
            fields["trace_id"] = req.trace_id
        if req.span_id:
            fields["span_id"] = req.span_id

        # 常驻字段最后写入 → 覆盖同名 extra，保证统一。
        fields.update({
            "service": self._service,
            "env": self._env,
            "instance": self._instance,
            "community": community,
            "level": record.levelname.lower(),
            "msg": record.getMessage(),
            "time": time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime(record.created))
            + f".{int(record.msecs):03d}Z",
        })

        # 异常堆栈附 error 字段。
        if record.exc_info:
            fields["error"] = self.formatException(record.exc_info)

        return json.dumps(fields, ensure_ascii=False, default=str)


def _serialize(value: Any) -> Any:
    try:
        json.dumps(value)
        return value
    except (TypeError, ValueError):
        return str(value)


class JsonHandler(logging.StreamHandler):
    """把日志写到 stream 的 handler，自动用 JsonFormatter。"""

    def __init__(self, stream: Any, *, service: str, env: str, instance: str,
                 community: str) -> None:
        super().__init__(stream)
        self.name = _SDK_HANDLER_NAME
        self.setFormatter(JsonFormatter(
            service=service, env=env, instance=instance, community=community))


# 进程级配置。get_logger 延迟 init 到首次调用。
_defaults: dict[str, str] | None = None
_log_level = logging.INFO


def init(*, service: Optional[str] = None, env: Optional[str] = None,
         instance: Optional[str] = None, community: Optional[str] = None,
         level: str = "info", stream: Any = sys.stdout) -> None:
    """初始化日志：注入常驻字段，在 root logger 挂 JSON handler。进程内幂等。

    重复 init 会用新配置重建（替换旧 SDK handler）。
    """
    global _defaults, _log_level
    _defaults = {
        "service": _env.service(service),
        "env": _env.env(env),
        "instance": _env.instance(instance),
        "community": _env.community(community),
    }
    _log_level = getattr(logging, level.upper(), logging.INFO)

    root = logging.getLogger()

    # 移除旧 SDK handler，挂新配置的。
    for h in list(root.handlers):
        if getattr(h, "name", None) == _SDK_HANDLER_NAME:
            root.removeHandler(h)
    handler = JsonHandler(stream, **_defaults)
    handler.setLevel(_log_level)
    root.addHandler(handler)


def get_logger(name: Optional[str] = None) -> logging.Logger:
    """返回一个可直接打点的 logger。

    命名 logger 经 propagate 落到 root 上 SDK 挂的 JSON handler，单条日志只输出一次。
    不传名字时返回 SDK 自己的 logger（**不是 root**）。

    级别只设在 SDK 交出的这个 logger 上，不碰 root：root 是宿主应用的全局开关，
    把它的 level 压到 DEBUG（此前行为）会让应用自己挂在 root 上的 handler 也开始
    收到 DEBUG 记录 —— 一个 SDK 不该改动宿主的全局日志级别。代价是第三方库
    （uvicorn / werkzeug 等）的日志级别由应用自己的配置决定，不再被 SDK 放宽。

    返回的 logger 级别由 init(level=...) 决定；应用如需另行调整，自行 setLevel 即可。
    """
    if _defaults is None:
        init()
    logger = logging.getLogger(_SDK_LOGGER_NAME if name is None else name)
    logger.setLevel(_log_level)
    return logger


# --- 便捷函数（命名 = 调用方模块名） ---

def debug(msg: str, *args: Any, **kwargs: Any) -> None:
    get_logger().debug(msg, *args, **kwargs)


def info(msg: str, *args: Any, **kwargs: Any) -> None:
    get_logger().info(msg, *args, **kwargs)


def warn(msg: str, *args: Any, **kwargs: Any) -> None:
    get_logger().warning(msg, *args, **kwargs)


def error(msg: str, *args: Any, **kwargs: Any) -> None:
    get_logger().error(msg, *args, **kwargs)
