"""框架适配器：在入口把可信解析的请求级字段 bind 进 context，并挂 /metrics。

设计约束（spec/common-fields.md）：community 请求覆盖必须来自可信判定点
（路由前缀 / 认证主体 / 白名单）。SDK 提供 bind 入口，由业务提供
community_resolver；SDK 不裸透传外部入参。

三个适配器共享同一套「取入站 request_id + 可选 community」逻辑，区别仅在
框架挂载 API。框架未安装时导入对应函数会 ImportError，业务按其实际依赖
选装（见 pyproject optional-dependencies）。

/metrics 路由不在本文件实现：业务按各自框架加一条返回
metrics.generate_text() 的路由即可（见各函数 docstring 示例）。
"""

from __future__ import annotations

import uuid
from typing import Callable, Optional

from . import _context

# 可信入站请求 ID 头（沿用 Go 版常量）。
HEADER_REQUEST_ID = "X-Request-Id"

# community 解析函数：入参为各框架 request 对象，返回社区字符串或 None。
Resolver = Callable[[object], Optional[str]]


def make_request_id(inbound: Optional[str]) -> str:
    """沿用入站 request_id，否则生成。"""
    return inbound or uuid.uuid4().hex


def bind_request(request, community: Optional[str]) -> "_context._GeneratorContextManager":
    """构造 _context.bind 上下文管理器（含 request_id 生成）。

    request 需提供 .headers（dict 兼容，含 .get）。
    """
    rid = request.headers.get(HEADER_REQUEST_ID)
    return _context.bind(community=community, request_id=make_request_id(rid))


# ---------------------------------------------------------------------------
# FastAPI（ASGI，基于 starlette BaseHTTPMiddleware）。
# ---------------------------------------------------------------------------


def fastapi_wrap(app, resolver: Optional[Resolver] = None):
    """为 FastAPI app 挂载观测中间件，返回原 app（已加中间件）。

    用法：

        from fastapi import FastAPI, Response
        from obs_sdk import metrics, middleware as mw

        app = FastAPI()
        mw.fastapi_wrap(app, resolver=lambda request: "openeuler")

        @app.get("/metrics")
        def metrics_route():
            return Response(metrics.generate_text(), media_type=metrics.content_type())
    """
    from starlette.middleware.base import BaseHTTPMiddleware

    class ObsMiddleware(BaseHTTPMiddleware):
        async def dispatch(self, request, call_next):
            community = resolver(request) if resolver else None
            with bind_request(request, community):
                return await call_next(request)

    app.add_middleware(ObsMiddleware)
    return app


# ---------------------------------------------------------------------------
# Flask（WSGI，before/teardown）。
# ---------------------------------------------------------------------------


def flask_middleware(app, resolver: Optional[Resolver] = None):
    """为 Flask app 挂载观测 before/after 钩子。

    用法：

        from flask import Flask, Response
        from obs_sdk import metrics, middleware as mw

        app = Flask(__name__)
        mw.flask_middleware(app, resolver=lambda request: "openeuler")

        @app.get("/metrics")
        def metrics_route():
            return Response(metrics.generate_text(), mimetype=metrics.content_type())
    """
    import flask

    @app.before_request
    def _obs_before():
        community = resolver(flask.request) if resolver else None
        ctx = bind_request(flask.request, community)
        flask.g._obs_ctx = ctx
        ctx.__enter__()

    @app.teardown_request
    def _obs_teardown(exc=None):
        ctx = getattr(flask.g, "_obs_ctx", None)
        if ctx is not None:
            ctx.__exit__(None, None, None)

    return app


# ---------------------------------------------------------------------------
# Django（WSGI middleware）。
# ---------------------------------------------------------------------------


class DjangoMiddleware:
    """Django 观测中间件（请求级字段 bind + request_id 注入）。

    用法（settings.MIDDLEWARE 追加）：

        "obs_sdk.middleware.DjangoMiddleware",

    多社区中心化服务覆写 resolve_community(request) 按可信来源返回社区。
    """

    def __init__(self, get_response: Callable):
        self.get_response = get_response

    def __call__(self, request):
        community = self.resolve_community(request)
        with bind_request(request, community):
            return self.get_response(request)

    def resolve_community(self, request) -> Optional[str]:
        """覆写点：按可信来源返回社区；默认不覆盖（用部署级默认）。"""
        return None
