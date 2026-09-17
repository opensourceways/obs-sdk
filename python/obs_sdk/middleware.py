"""框架适配器：在入口把可信解析的请求级字段 bind 进 context，并记 HTTP 服务端指标。

设计约束（spec/common-fields.md）：community 请求覆盖必须来自可信判定点
（路由前缀 / 认证主体 / 白名单）。SDK 提供 bind 入口，由业务提供
community_resolver；SDK 不裸透传外部入参。

服务端指标（spec/metrics-format.md「默认暴露的中间件指标」）：三个适配器都会记
`obs_http_server_requests_total` / `obs_http_server_request_duration_seconds`，
与 Go / Node 中间件语义一致。**path label 取路由模板**（如 `/items/{item_id}`），
不是原始 URL —— 原始路径带 ID 会撑爆时序基数；拿不到模板时记为 `unmatched`。
不需要 SDK 记这两个指标时传 `collect_server_metrics=False`（服务自己已有
等价 instrumentation 的情况）。

三个适配器共享同一套「取入站 request_id + 可选 community + 记服务端指标」逻辑，
区别仅在框架挂载 API。框架未安装时导入对应函数会 ImportError，业务按其实际依赖
选装（见 pyproject optional-dependencies）。

/metrics 路由不在本文件实现：业务按各自框架加一条返回
metrics.generate_text() 的路由即可（见各函数 docstring 示例）。
"""

from __future__ import annotations

import time
import uuid
from typing import Callable, Optional

from . import _context, metrics as obs_metrics

# 可信入站请求 ID 头（沿用 Go 版常量）。
HEADER_REQUEST_ID = "X-Request-Id"

# community 解析函数：入参为各框架 request 对象，返回社区字符串或 None。
Resolver = Callable[[object], Optional[str]]

# 路由模板取不到时的 path label 取值（404 / 未匹配）。
UNMATCHED_PATH = "unmatched"


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
# 服务端指标装配（三个适配器共用）
# ---------------------------------------------------------------------------


def _http_server(metrics, collect: bool) -> Optional["obs_metrics._HttpServer"]:
    """取 HTTP 服务端指标句柄。

    metrics 为 None 时用进程级单例（与 log/metrics 模块级函数的懒初始化语义一致），
    这样按文档 `fastapi_wrap(app, resolver=...)` 直接接就能拿到服务端指标。
    """
    if not collect:
        return None
    target = obs_metrics.default() if metrics is None else metrics
    return target.http_server()


def _route_template(request) -> str:
    """取路由模板（低基数）。三个框架的取法不同，统一在这里兜一遍。

    顺序无所谓——每种框架只会命中其中一种；都取不到（如 404）时返回 unmatched。
    """
    # Starlette / FastAPI：router 匹配后把 route 写进 scope（call_next 返回后可见）。
    scope = getattr(request, "scope", None)
    if isinstance(scope, dict):
        route = scope.get("route")
        template = getattr(route, "path", None)
        if template:
            return template

    # Flask：url_rule.rule 即模板（/items/<int:item_id>）。
    rule = getattr(getattr(request, "url_rule", None), "rule", None)
    if rule:
        return rule

    # Django：resolver_match.route 即模板（items/<int:pk>/）。
    route = getattr(getattr(request, "resolver_match", None), "route", None)
    if route:
        return route

    return UNMATCHED_PATH


def _record(server, request, status_code: int, start: float) -> None:
    """记一次请求。

    必须在请求上下文仍绑定时调用：community label 由 _Vec 从当前上下文取，
    上下文一 reset 就只剩部署默认值了。
    """
    if server is None:
        return
    server.observe_request(
        method=request.method,
        path=_route_template(request),
        status_code=status_code,
        seconds=time.perf_counter() - start,
    )


# ---------------------------------------------------------------------------
# FastAPI（ASGI，基于 starlette BaseHTTPMiddleware）。
# ---------------------------------------------------------------------------


def fastapi_wrap(app, resolver: Optional[Resolver] = None, metrics=None,
                 collect_server_metrics: bool = True):
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

    server = _http_server(metrics, collect_server_metrics)

    class ObsMiddleware(BaseHTTPMiddleware):
        async def dispatch(self, request, call_next):
            community = resolver(request) if resolver else None
            start = time.perf_counter()
            with bind_request(request, community):
                try:
                    response = await call_next(request)
                except Exception:
                    # 视图抛异常：无响应对象，按 500 记一次再原样抛出。
                    _record(server, request, 500, start)
                    raise
                _record(server, request, response.status_code, start)
                return response

    app.add_middleware(ObsMiddleware)
    return app


# ---------------------------------------------------------------------------
# Flask（WSGI，before/after/teardown）。
# ---------------------------------------------------------------------------


def flask_middleware(app, resolver: Optional[Resolver] = None, metrics=None,
                     collect_server_metrics: bool = True):
    """为 Flask app 挂载观测钩子。

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

    server = _http_server(metrics, collect_server_metrics)

    def record_once(status_code: int) -> None:
        # 没走过 before_request（如更早的钩子直接返回）时 _obs_start 不存在，跳过。
        start = getattr(flask.g, "_obs_start", None)
        if start is None or getattr(flask.g, "_obs_recorded", True):
            return
        flask.g._obs_recorded = True
        _record(server, flask.request, status_code, start)

    @app.before_request
    def _obs_before():
        ctx = bind_request(flask.request, resolver(flask.request) if resolver else None)
        flask.g._obs_ctx = ctx
        flask.g._obs_start = time.perf_counter()
        flask.g._obs_recorded = False
        ctx.__enter__()

    @app.after_request
    def _obs_after(response):
        record_once(response.status_code)
        return response

    @app.teardown_request
    def _obs_teardown(exc=None):
        if exc is not None:
            # 视图抛异常时 after_request 不执行，在这里补记 500，避免漏计。
            record_once(500)
        ctx = getattr(flask.g, "_obs_ctx", None)
        if ctx is not None:
            ctx.__exit__(None, None, None)

    return app


# ---------------------------------------------------------------------------
# Django（WSGI / ASGI middleware）。
# ---------------------------------------------------------------------------


class DjangoMiddleware:
    """Django 观测中间件（请求级字段 bind + request_id 注入 + 服务端指标）。

    用法（settings.MIDDLEWARE 追加）：

        "obs_sdk.middleware.DjangoMiddleware",

    多社区中心化服务覆写 resolve_community(request) 按可信来源返回社区。
    不需要 SDK 记服务端指标时，子类里把 collect_server_metrics 置 False。
    """

    #: 子类可覆写为 False 关闭 SDK 的服务端指标（服务已有等价 instrumentation 时）。
    collect_server_metrics = True

    def __init__(self, get_response: Callable):
        self.get_response = get_response
        self._server = _http_server(None, self.collect_server_metrics)

    def __call__(self, request):
        community = self.resolve_community(request)
        start = time.perf_counter()
        with bind_request(request, community):
            try:
                response = self.get_response(request)
            except Exception:
                _record(self._server, request, 500, start)
                raise
            _record(self._server, request, response.status_code, start)
            return response

    def resolve_community(self, request) -> Optional[str]:
        """覆写点：按可信来源返回社区；默认不覆盖（用部署级默认）。"""
        return None
