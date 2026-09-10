# 通用字段契约 — service / env / instance / community / request_id / trace_id / span_id

> 7 个通用字段是所有 log 与 metrics 的公共标识维度。字段语义、来源、注入规则在此统一。

## 字段总表

| 字段 | 语义 | 类型 | 注入层级 | 静态/动态 |
| --- | --- | --- | --- | --- |
| `service` | 服务名（微服务名 / module 子目录名） | string | Init（进程级） | 静态 |
| `env` | 部署环境 | string | Init（进程级） | 静态 |
| `instance` | 实例标识（pod 名 / IP / hostname） | string | Init（进程级，推荐 k8s `metadata.name` 或 hostname） | 静态 |
| `community` | 社区标识 | string | Init（进程级默认） **+** 请求上下文（覆盖） | 双态 |
| `request_id` | 单请求关联 ID | string | 请求上下文 | 动态 |
| `trace_id` | 分布式 trace ID（二期接入） | string | 请求上下文（预留） | 动态/预留 |
| `span_id` | 分布式 trace 内的 span 标识（二期接入） | string | 请求上下文（预留） | 动态/预留 |

## 静态字段来源与默认值

进程启动 Init 时注入，进程生命周期不变。推荐按优先级读取：

1. SDK Init 显式参数（最高优先级，测试/单实例可控）；
2. 部署环境变量（见下）；
3. 内置默认（`service=unknown`、`env=unknown`、`instance=hostname`）。

| 字段 | 推荐环境变量名 | 说明 |
| --- | --- | --- |
| `service` | `OBS_SERVICE` | 部署时由编排（helm/kustomize）写入 |
| `env` | `OBS_ENV` | prod / test / preview / staging |
| `instance` | `OBS_INSTANCE`（缺省取 hostname） | k8s 下可用 `metadata.name`（=pod 名）经环境变量注入 |
| `community` | `OBS_COMMUNITY` | **部署级默认 community**：单社区服务常驻该值 |

> 变量名统一 `OBS_*` 前缀；四语言 SDK 均读同一套环境变量名，保证部署配置模板（helm values）不因语言而异。

## community 双层注入（本需求核心）

两个场景，共用同一套字段/label 注册，靠取值来源区分：

### 第一层：部署级默认注入（静态）

- 场景：**服务按社区拆实例**（如 openEuler 一份、MindSpore 一份各自独立部署），每实例只服务一个社区。
- 行为：Init 从 `OBS_COMMUNITY`（或显式参数）注入默认 community。
  - **日志**：作为常驻字段打底，出现在该进程所有日志行。
  - **指标**：作为**常驻 const label** 打进该进程所有时间序列。
- 请求处理中若无请求级覆盖，一律使用该默认值。

### 第二层：请求上下文动态覆盖（动态）

- 场景：**中心化单实例服务多社区**（一个部署同时服务 openEuler / MindSpore 等多个社区，靠路由/鉴权识别请求归属社区）。典型：community-robots 类中心 hub、message-bus。
- 行为：SDK 中间件或业务代码在**请求上下文**里设置覆盖值 `community=xxx`；处理该请求时：
  - **日志**：该请求关联的所有日志行 community 字段 = 覆盖值。
  - **指标**：该请求触发打点的时间序列其 community label = 覆盖值（需用动态 label，见 metrics-format.md）。
- 覆盖值缺失 → 回退第一层默认值。

### 注入路径约束（安全相关）

- **请求级 community 永不从不可信入参盲取**（如裸读 URL/Header 即当 community）。必须来自：服务自己按可信来源（路由路径前缀、认证后的主体、白名单表）判定后显式放入上下文的**类型化值**。SDK 只负责读上下文里**已经放好的类型化值**，不负责从外部请求原样透传。
- 四个语言 SDK 提供一致的「往上下文放 community / 从上下文读 community」API，供业务在可信判定点写入。

## request_id 注入

- 来源优先级：可信入站头（如 `X-Request-Id`，**在服务已校验可信时**）＞ 中间件自动生成（UUID/雪花）＞ 无。
- SDK 中间件：入口中间件若上下文无 request_id 则生成并写入；日志绑定上下文时带上。
- 出站调用传播：作为头/字段传给下游服务（语言 SDK 提供 outbound 侧 helper），保证全链路同 ID。

## trace_id / span_id 预留位

- 二期接 OpenTelemetry 后，trace / span 经 context 注入；日志上下文里的 `trace_id`、`span_id` 即取自该 context。
- 首期：两个注入位必须存在（log 上下文 API 有对应字段位置、metrics label 有对应位但可省略），值恒空。
  目标：二期 trace 接入时**零日志格式返工**。
- 两者都是**高基数**值（每请求/每 span 唯一），只入日志，**禁止作 metrics label**（见 metrics-format.md）。
