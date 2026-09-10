"""请求级通用字段的 context 读写（Python 版 sdkctx）。

用 contextvars 实现（线程 / async 各自隔离，与 spec/common-fields.md 一致）：
  - community 覆盖值 / request_id / trace_id 存于当前 Context；
  - 框架适配器在入口把可信解析出的字段 bind 进 Context，退出时 reset；
  - log / metrics 读取当前 Context，未绑定则回退部署级默认。
"""

from __future__ import annotations

import contextvars
import dataclasses
from contextlib import contextmanager
from typing import Iterator


@dataclasses.dataclass
class Request:
    """请求级通用字段。空串 / None 表示未设置。"""

    community: str | None = None
    request_id: str | None = None
    trace_id: str | None = None


_current: contextvars.ContextVar[Request] = contextvars.ContextVar(
    "obs_request", default=Request()
)


def get() -> Request:
    """读取当前请求级字段；未设置返回空 Request。"""
    return _current.get()


def community() -> str | None:
    """当前 community 覆盖值；未设置返回 None。"""
    return _current.get().community


def request_id() -> str | None:
    return _current.get().request_id


def trace_id() -> str | None:
    return _current.get().trace_id


@contextmanager
def bind(*, community: str | None = None, request_id: str | None = None,
         trace_id: str | None = None) -> Iterator[None]:
    """把请求级字段 bind 进当前 Context；退出自动 reset。

    用于框架适配器入口 / 业务可信判定点：

        with bind(community="openeuler", request_id=req_id):
            logger.info("...")   # 自动带 community/request_id
    """
    prev = _current.get()
    merged = Request(
        community=community if community is not None else prev.community,
        request_id=request_id if request_id is not None else prev.request_id,
        trace_id=trace_id if trace_id is not None else prev.trace_id,
    )
    token = _current.set(merged)
    try:
        yield
    finally:
        _current.reset(token)
