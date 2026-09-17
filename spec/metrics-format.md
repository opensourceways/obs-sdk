# 指标格式契约 — 命名与 label 规范

> 采集链路：服务暴露 `/metrics`（Prometheus text 格式）→ AOM 2.0 Prometheus 实例（ServiceMonitor 抓取）→ 大盘/告警。格式统一是跨服务聚合的前提。

## 指标命名前缀

Prometheus 指标命名规则基础上追加组织约束：

- 指标名全小写，`snake_case`，单位后缀遵循 Prometheus 约定（`_total` / `_seconds` / `_bytes` / `_count` / `_sum`）。
- **业务指标强制前缀**：`<service>_`（service 短名，服务唯一）。例：`robot-universal-review_http_requests_total` → 简写 `review_http_requests_total`。
  - 也可按服务内子系统细分：`<service>_<subsystem>_<name>_<unit>`。
- **中间件公共指标不加任何前缀**，直接用 `http_server_*` 这类通用名（见下节表）。既不加 `<service>_`，也不加 SDK 保留前缀（如 `obs_`）：
  - 同一条时间序列已带 `service` label，查询按 label 过滤 —— 名字里再编一遍前缀是重复信息；
  - 带上前缀会让「同一指标名下的跨服务查询」失效，每个服务得各写一套名字；
  - Actuator / Micrometer 等**官方 instrumentation 的默认名不带这类前缀**（如 `http_server_requests_seconds`），自造前缀需要额外做一层名字映射，与「薄封装、不自研 instrumentation」冲突。

## 通用 label 集（每个时间序列必须带）

SDK 统一为所有注册的指标自动附加以下 **const label**（值来自 Init 注入的静态字段）：

| label | 值来源 | 说明 |
| --- | --- | --- |
| `service` | Init | 服务名 |
| `env` | Init | 环境 |
| `instance` | Init | 实例标识 |
| `community` | Init 默认 **+ 请求级覆盖** | 见下节——唯一一个可动态的公共 label |

> 其余业务维度 label（`endpoint` / `method` / `status` / `code` 等）由业务/中间件按需声明。
> **禁止**把 `request_id` / `trace_id` / `span_id` 等高基数值当 label——会撑爆时序基数。

## community 双层注入在指标上的实现

同一个注册、两套取值来源：

- **静态默认**：初始化时把默认 community 作为 const label 打进注册表。单社区独立部署服务到此为止，全部时间序列带固定 `community`。
- **动态覆盖**：中心化服务需要区分社区时——SDK 指标 API 的**打点函数接受可选 community 覆盖**（与日志绑定式/参数式一致）：
  - 实现方式（推荐，兼容官方库）：对需要区分 community 的业务指标，注册时把 `community` 声明为**普通可变 label**（同时保留 service/env/instance 作 const label），打点时由 SDK 从上下文/覆盖参数取出 community 值填 label。
  - 即：**同一条指标**，单社区场景填默认值即可；多社区场景填覆盖值。注册一次，两用。
- 若官方库只支持 const label（无法按请求覆盖），SDK 应暴露「注册时声明该指标 community 是否动态」的两套方法，内部按官方库能力映射到 const label 或普通 label。

## 默认暴露的中间件指标（可选，按服务依赖挑）

| 指标 | 类型 | label |
| --- | --- | --- |
| `http_server_requests_total` | Counter | service, env, instance, community, method, path(可选,注意基数), status_code |
| `http_server_request_duration_seconds` | Histogram | service, env, instance, community, method, path(可选), status_code |
| `log_entries_total` | Counter | service, env, instance, community, level |

> 不强制：服务只要保证**自己注册的业务指标**带通用 label 即可；中间件公共指标（若启用）用上述通用名，
> 且**路径不入 label 或按低基数路由模板入 label**（`/items/{id}`，不是 `/items/123`）。
>
> `status_code` 在 counter 与 histogram 上**都带**：按状态码看时延（如只看 5xx 的 P99）是常见诉求，
> 只在 counter 上带会让这个查询无法表达。
>
> 同一指标在**各语言 SDK 间名字必须一致**；Java 走 Actuator 时 Micrometer 的默认名为
> `http_server_requests_seconds`（label 用 `status` / `uri`，无 `_total`），与上表不同 —— 这是官方
> instrumentation 的既定形状，SDK 不做名字映射（见「服务要求」的多语言同构边界）。

## 服务要求

- 每个服务暴露一个统一 `/metrics` HTTP 端点（Prometheus text exposition）。
- 抓取协议：HTTP `GET /metrics`，官方库默认 behavior，content-type `text/plain; version=0.0.4`。
- 多语言同构：四个语言 SDK 都要能输出**语义等价**的 `service/env/instance/community` 组合，供 ServiceMonitor 统一抓取、AOM/Cortex 统一查询。
  - 同构的**边界**：`service/env/instance/community` 这套通用 label 与「不重复埋点」的官方 instrumentation 是强约束；
    服务端指标的**名字与其余 label** 则由各语言官方库的既定形状决定，SDK 只做装配、不改名。
    因此 Java（Actuator + Micrometer）的 `http_server_requests_seconds` / `status` / `uri` 与
    Go / Python / Node 的 `http_server_requests_total` / `status_code` / `path` 并不逐字相同。
    跨语言统一查询请对齐**通用 label**，不要把服务端指标名写进跨语言的告警规则。
