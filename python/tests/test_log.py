"""log 模块单测：静态字段注入、community 双层注入、level 过滤、trace_id 预留。"""

import io
import json
import logging
from typing import Dict, List

import pytest

from obs_sdk import log


@pytest.fixture(autouse=True)
def _reset_log():
    # 每个用例前重置进程级配置，保证测试隔离。
    log._defaults = None
    log._log_level = logging.INFO
    root = logging.getLogger()
    for h in list(root.handlers):
        if getattr(h, "name", None) == log._SDK_HANDLER_NAME:
            root.removeHandler(h)
    yield
    # 恢复默认 stdout，避免污染后续。
    log._defaults = None
    for h in list(root.handlers):
        if getattr(h, "name", None) == log._SDK_HANDLER_NAME:
            root.removeHandler(h)


def _capture(service: str = "srv", community: str = "openeuler",
             level: str = "info") -> List[Dict]:
    buf = io.StringIO()
    log.init(service=service, community=community, level=level, stream=buf)
    return buf


def _lines(buf: io.StringIO) -> List[Dict]:
    return [json.loads(line) for line in buf.getvalue().strip().splitlines()]


def test_static_fields_injected():
    buf = _capture(service="review", community="openeuler")
    logger = log.get_logger("t")
    logger.info("hello", extra={"event": "pull_request"})

    lines = _lines(buf)
    assert len(lines) == 1
    f = lines[0]
    assert f["service"] == "review"
    assert f["community"] == "openeuler"
    assert f["level"] == "info"
    assert f["msg"] == "hello"
    assert f["event"] == "pull_request"
    # 无请求上下文时不输出 request_id / trace_id。
    assert "request_id" not in f
    assert "trace_id" not in f


def test_business_extra_overrides_nothing_common():
    buf = _capture(service="review")
    logger = log.get_logger("t")
    logger.info("x", extra={"service": "fake", "k": 1})
    f = _lines(buf)[0]
    # 业务 extra 不得覆盖常驻字段。
    assert f["service"] == "review"
    assert f["k"] == 1


def test_request_community_override():
    from obs_sdk import _context
    buf = _capture(community="openeuler")
    logger = log.get_logger("t")
    with _context.bind(community="mindspore", request_id="req-1"):
        logger.info("scoped")
    f = _lines(buf)[0]
    assert f["community"] == "mindspore"
    assert f["request_id"] == "req-1"


def test_level_filter():
    buf = _capture(service="srv", level="warn")
    logger = log.get_logger("t")
    logger.info("dropped")
    logger.warning("kept")
    lines = _lines(buf)
    assert len(lines) == 1
    assert lines[0]["msg"] == "kept"


def test_trace_id_reserved_inject():
    from obs_sdk import _context
    buf = _capture(community="openeuler")
    logger = log.get_logger("t")
    with _context.bind(trace_id="trace-xyz"):
        logger.info("with trace")
    f = _lines(buf)[0]
    assert f["trace_id"] == "trace-xyz"
    assert f["community"] == "openeuler"


def test_error_field_on_exception():
    buf = _capture(service="srv")
    logger = log.get_logger("t")
    try:
        raise ValueError("boom")
    except ValueError:
        logger.error("failed", exc_info=True)
    f = _lines(buf)[0]
    assert f["level"] == "error"
    assert "boom" in f["error"]


def test_json_time_format():
    buf = _capture(service="srv")
    log.get_logger("t").info("x")
    f = _lines(buf)[0]
    # RFC3339 UTC，毫秒级：2026-09-08T07:12:34.123Z
    import re
    assert re.match(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$", f["time"])
