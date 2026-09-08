# obs-sdk — 微服务可观测薄封装 SDK（monorepo）

> 需求：[opensourceways/backlog#1938](https://github.com/opensourceways/backlog/issues/1938) 微服务可观测建设 · 子任务：[#2061](https://github.com/opensourceways/backlog/issues/2061)

统一薄封装 SDK：日志 + 指标两个 package，内部用官方库，不自研 instrumentation。契约层单一事实来源。

## 目录结构
- `spec/` — 契约层（日志 JSON schema、通用字段、指标命名 prefix/label 规范）——单一事实来源
- `go/` — obs-sdk-go（log + metrics）
- `python/` — obs-sdk-python
- `java/` — obs-sdk-java
- `node/` — obs-sdk-node

各语言子目录自管版本（go.mod / pyproject.toml / pom.xml / package.json），Git tag 用 `go-v1.0.0` 等前缀区分。

> 骨架状态：仅建目录结构，实现见 [#2061](https://github.com/opensourceways/backlog/issues/2061)。
