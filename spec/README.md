# spec — 契约层（单一事实来源）

> 骨架占位。内容待 [#2061](https://github.com/opensourceways/backlog/issues/2061) 实现。

定义：
- 日志 JSON schema（结构、级别）
- 通用字段：`service` / `env` / `instance` / `community` / `request_id` / `trace_id`
- 指标命名 prefix 与 label 规范（含 `community` 双层注入：部署级默认 + 请求级覆盖）
- community 取值枚举（遵循 service.md：Ascend / CANN / MindSpore / openEuler / openGauss 等）
