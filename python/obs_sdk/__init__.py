"""obs-sdk-python：opensourceways 微服务可观测薄封装 SDK（log + metrics）。

契约见仓库顶层 `spec/`，本包按 spec 装配：
  - `obs_sdk.log`      结构化 JSON 日志（字段规范 spec/log-format.md）
  - `obs_sdk.metrics`  Prometheus 指标（label 规范 spec/metrics-format.md）
  - `obs_sdk._context` 请求级字段 bind（community 双层注入，spec/common-fields.md）
  - `obs_sdk.middleware` 框架适配器（FastAPI / Flask / Django）
"""

from . import _context, log, metrics, middleware

__all__ = ["log", "metrics", "middleware"]
