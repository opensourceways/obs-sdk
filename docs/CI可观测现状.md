# CI 可观测现状

> 快照时间：2026-10-22
> 数据来源：`opensourceways/ascend-ci-deployment` main（`4d8d539c`）与华为云 CES 控制面实查
> 配套文档：[可观测接入指南](可观测接入指南.md)

## 0. 结论先行

按三个层面看，**资源层与执行层基本完整，workflow 层只看到"正在跑"，看不到"跑得怎么样"，Jenkins 层还是空白**。

| 层面 | 现状 | 缺口 |
| --- | --- | --- |
| CI 执行资源 | 较完整：squid / registry-nginx / NPU / SFS / 缓存计量都有 | ARC 控制器指标未接入；出口计量只覆盖代理链路；node-exporter 无版本化管理 |
| CI workflow | 有现场信息（谁在跑什么）、版本、GitHub 连通性 | **没有任何结果指标**（成功 / 失败 / 失败率） |
| Jenkins 任务 | 只有云资源告警（节点 / 磁盘 / SFS / ELB） | **没有任何构建结果指标**，方案见接入指南 6.1 |

---

## 1. 整体架构

```
各业务集群（14 个）                        中心：infra-cn4-x86-common-cluster
──────────────────────────────────────    ────────────────────────────────────────────
Prometheus Agent（自建 Deployment）
  ├─ 拉取：cadvisor / KSM / node-exporter   ──remote_write──┐
  ├─ 拉取：squid / mtail / registry-nginx                  │
  └─ 拉取：NPU exporter / buildkitd-dispatcher             ▼
                                                    Prometheus :9090
CronJob 拨测（ci-probe 镜像）                       ├─ PrometheusRule 求值
  └─ push ──→ Pushgateway :9091 ──scrape────────────────┤  └─ Alertmanager → 邮件
                                                      └─ Grafana（2 张盘）
```

---

## 2. 资源与基础设施

| 采集项 | 覆盖 | 部署方式 | 能观测到什么 | 观测不到什么 |
| --- | --- | --- | --- | --- |
| cadvisor | 12 集群 | **Kubelet 自带**，本仓只提供抓取配置，抓 `:10250/metrics/cadvisor` | **Pod 级网络收发**：`container_network_{receive,transmit}_{bytes,packets}_total`、`..._packets_dropped_total`、`..._errors_total`；可按 `namespace` / `pod` / `interface` / `node` 下钻到单个 Pod 的进出流量、丢包与错误 | 容器 CPU、内存、文件系统、磁盘 IO（写入过滤只 `keep container_network_.*`）；单容器粒度（丢掉 `container!=""` 的行，只留 `container=""` 的 Pod 聚合） |
| kube-state-metrics | 12 集群 | 自建 Helm（社区 chart 5.28.1），**12 个 ArgoCD Application**；镜像 `modelfoundry/kube-state-metrics:v2.14.0`，Service `:8080`，NodePort `32080` | **K8s 对象状态**：Pod 相位 / 重启次数 / 容器等待与终止原因、Deployment·RS·STS·DS 的期望与就绪副本、Node Ready 条件与可分配资源、Job·CronJob 的成功失败与最后成功时间、PVC 相位、Service·Namespace·HPA 等对象数量；Pod / Deployment / Node 上**全部 K8s 标签**（`metricLabelsAllowlist: [*]`）以 `label_*` 暴露，可按自定义标签筛 | 资源**实际用量**（它是状态观测器，不产出 CPU / 内存消耗）；容器维度 |
| node-exporter | 9 集群 | 自建 Helm（`hostNetwork` + `hostPID`，`:9100`），**无 ArgoCD Application** | **节点主机级**：各 CPU mode 时间、内存可用量、文件系统容量与 inode、磁盘读写与 IO 时间、网卡收发 / 错误 / 丢包、`node_load*`；已排除 `/dev` `/proc` `/sys` 与 docker / kubelet 内部挂载点 | Pod / namespace 维度（只有节点 IP，只能定位到机器）；且实际只在 9 个集群采——`guiyang-001` / `guiyang-002` / `wlcb-001` 把该 job 注释掉了 |

对应看板：`CI-Network-Overview`、`Pod-Network-Traffic`（`monitoring/grafana/dashboards/`）。

> 前两项（cadvisor、kube-state-metrics）的 Pod 指标都带 `namespace` 标签，而每个集群的 `remote_write` 都有一条
> `namespace =~ "ascend-gha-runners|ascend-gha-runners-gy-006" → drop`。relabel 正则是整体锚定的，所以命名空间**恰好叫**
> `ascend-gha-runners` 的 runner Pod，其网络与状态指标到不了中心；带后缀的（如 `ascend-gha-runners-hk-001`）不受影响。
> runner 是否可见取决于**命名空间叫什么**，而不是它是不是 runner。

---

## 3. CI 执行资源

| 组件 | 覆盖 | 数据来源 | 关注点 |
| --- | --- | --- | --- |
| squid 缓存 | 13 集群 | squid exporter `:9301` + mtail `:3901` | 命中率、回源错误、磁盘与 fd |
| registry-nginx | 13 集群 | nginx exporter `:9113` | 镜像仓库真实错误率、命中率 |
| NPU 卡池 | 12 集群 | 自建 exporter `:9101` + 官方 exporter `:8082`（pod 发现） | 卡池利用率、排队、空闲占卡 |
| buildkitd-dispatcher | 1 集群（guiyang-006） | 自研 `/metrics:8080` | 构建并发水位 |
| nginx-pypi-cache 计量 | 有该组件的集群 | CronJob 每 2 分钟读 Pod stdout 日志（`upstream_bytes` / `client_bytes`）→ Pushgateway | 外网拉取与内网下发的字节速率、命中率 |
| Pod 粒度出口 | 同上 | CronJob 每 5 分钟解析 pypi-cache access log + **squid access.log**（`ssl_bump` 解密后有完整域名），Pod IP 经 k8s API join 成 namespace/pod | 谁、从哪个外网点、拉了多少（top 40） |
| SFS Turbo 共享盘 | 全集群 | CronJob 每 10 分钟自动发现 `sfsturbo.csi.everest.io` PV，按 share 去重，`chroot` + `mount.nfs` + `df` | 容量、使用率、mount 健康 |

出口计量的口径要写清楚：**只覆盖经过代理 / 缓存的流量**，直连外网的不在口径内。

---

## 4. CI workflow（runner 维度）

全部是 `monitoring/base/` 下的 CronJob，同一镜像 `ci-probe:1.0`，结果推 Pushgateway：

| 能力 | CronJob | 覆盖 | 频率 | 数据来源 |
| --- | --- | --- | --- | --- |
| `ci_runner_job` | `runner-job-probe` | 14 集群 | 每 2 分钟 | list `EphemeralRunner`，读定制 ARC 写入 `status` 的 `workflowRunId` / `jobRepositoryName` / `jobDisplayName` / `jobWorkflowRef` |
| runner 版本 | `runner-version-check` | 14 集群 | 每天 06:00 | 对去重镜像起一次性 Pod 跑 `run.sh --version`，与 GitHub 最新 release 比对 |
| GitHub 连通性 | `github-probe` | 14 集群 | 每 5 分钟 | `git clone --depth=1` 测试仓，分代理 / 直连两条路径 |
| GitHub 官方状态 | `github-status` | **仅 hk-001** | 每 5 分钟 | githubstatus.com Statuspage API，失败时降级探 `api.github.com` 推断事故等级 |

要点：

- `ci_runner_job` 是 **info 型指标（值恒为 1）**，同一 runner 输出两行（`pod=<runner>` 与 `pod=<runner>-workflow`），便于与 cadvisor 的 `container_network_*` 按 `(cluster, namespace, pod)` 关联（前提是该 runner 命名空间没被第 2 章的 `namespace` 丢弃规则挡掉）。Pushgateway 全量替换语义使 runner 结束后指标自然消失。
- **强依赖定制 ARC**：上述 status 字段上游原版没有，升级成社区版会直接断掉采集。
- `github-status` 的降级逻辑解决了"GitHub 大故障时状态页自己也会过载"的失明问题，用于区分**GitHub 事故还是我方问题**。
- hk-001 的 `github-probe` 被 patch 成只测直连（香港无墙，不走 git-cdn），告警判据也按集群分开。

---

## 5. Jenkins 任务（openEuler 社区门禁）

当前状态：**零指标**。

| 已有 | 内容 |
| --- | --- |
| 云资源告警（CES + SMN） | 16 条，覆盖 73 个构建节点、285 块 EVS 数据盘、8 个 SFS Turbo、23 个 ELB；主题 `alarm-openeuler-jenkins-ci` |
| 日志 | Jenkins 跑在 ECS / BMS 上，不在 K8s 内，不进 LTS 的集群采集链路 |
| 构建结果 | **无**，只有人工看 PR 标签或 Jenkins 页面 |

缺口的具体表现：2026 年国庆期间门禁因净室出口白名单未同步 DNS 变更而连续失败 8 天，**期间所有资源告警全绿**——CPU、内存、磁盘、SFS、ELB 均正常，故障属于"依赖契约断裂"，资源型告警天然不可见。

接入方案（自研 exporter 拉 Jenkins REST API，输出构建结果指标与告警）见[可观测接入指南 6.1](可观测接入指南.md#61-指标接入)，尚未落地。

---

## 6. 告警清单

中心 `prometheus-rules.yaml` 40 条，按主题分组：

| 主题 | 条数 | 告警 |
| --- | --- | --- |
| 探针存活 + GitHub 连通性与状态 | 10 | `ProbeStale`、`GitHubProxyUnreachable`、`GitHubDirectUnreachable`、`GitHubUnreachable`、`GitHubProbeMissing`、`GitHubStatusOutage`、`GitHubStatusComponentDegraded`、`GitHubStatusProbeStale`、`GitHubStatusPageUnreachable`、`GitHubStatusIncidentOpen` |
| Runner 版本与运行时长 | 4 | `RunnerUpdateAvailable`、`RunnerVersionExpiring`、`RunnerCrashLooping`、`WorkflowRunTooLong` |
| Runner / Workflow 调度 | 4 | `RunnerPodPendingTooLong`、`WorkflowPodPendingTooLong`、`RunnerImagePullFailed`、`RunnerInvalidImageName` |
| NPU 卡池 | 9 | `NPUCardDropped`、`ListenerCrashLooping`、`NPU8CardJobWaitTooLong`、`NPU8CardJobQueueStorm`、`NPUSmallJobWaitTooLong`、`NPUQueueProbeStale`、`NPUNodeIdleTooLong`、`NPUMetricTotalUsedCountMissing`、`NPUMetricTotalMissing` |
| 存储 / 证书 / 云账号 | 7 | `SharedDiskHighUsage`、`SharedDiskMountFailed`、`SfsTurboDiskProbeStale`、`CertExpiring`、`CertProbeFailed`、`CloudAccountLowBalance`、`CloudAccountUnreachable` |
| 平台与安全 | 6 | `SAExcessiveClusterAdmin`、`SAAuditAPIUnreachable`、`NginxConfDrift`、`NginxPodDown`、`InfraServiceCrashLooping`、`AlertDeliverySLOBreach` |

`AlertDeliverySLOBreach` 监控的是**告警本身能不能送达**；`ProbeStale` 系列监控的是**探针还活着没有**——两者都是防止"静默失效"的元监控。

squid 专项另 20 条（`squid-rules.yaml`）：目标/进程存活、指标缺失、重启、swap 风暴、磁盘、fd 耗尽、上游失败率、命中率崩塌、超时突发、registry 真实错误率等。

---

## 7. 日志

| 来源 | 链路 | 检索维度 |
| --- | --- | --- |
| CI 集群内组件 | fluent-bit → LTS | `cluster` + `namespace` + `pod`；runner namespace 为 `ascend-gha-runners*` |
| Jenkins（ECS / BMS） | **未纳入** | — |

日志目前只做**事后归因**，不参与告警触发（延迟与匹配成本都高）。

---

## 8. 缺口汇总

| # | 缺口 | 影响 | 建议 |
| --- | --- | --- | --- |
| 1 | workflow 无结果指标 | 无法计算成功 / 失败 / 失败率，批量失败只能靠人发现 | 短期接 pod-history-api 或自建结果 exporter；长期在 ARC 侧补 `EphemeralRunner` 终态导出 |
| 2 | ARC 控制器 `/metrics` 未采集 | `grafana-dashboard-arc.yaml` 的 26 个面板**大概率是空盘** | 每集群加一个 scrape job（先确认 `:8080` 连通与前缀） |
| 3 | node-exporter 无 ArgoCD 管理 + 静态 IP | 重装后静默丢数据；扩缩容要手工改 12 份 patch | 补 ArgoCD Application；给 `monitoring-sa` 加 `nodes list` 改动态发现 |
| 4 | Jenkins 结果观测为零 | 国庆类故障完全不可见 | 按接入指南 6.1 落地 |
| 5 | `mirror-sync` 已停用 | `kustomization.yaml` 中注释，盘上可能留着永不更新的旧线 | 确认是否还需要，不需要就把目录一起删掉 |
| 6 | 出口计量只覆盖代理链路 | 直连外网流量不在口径内 | 与云侧 CES / 流日志口径对齐，明确看板口径说明 |

---

## 9. 文件索引

| 内容 | 路径（`ascend-ci-deployment`） |
| --- | --- |
| 采集与拨测公共层 | `monitoring/base/` |
| 逐集群覆写 | `monitoring/config-for-<cluster>/` |
| 中心告警规则 | `monitoring/config-for-infra-cn4-x86-common-cluster/prometheus-rules.yaml`、`squid-rules.yaml` |
| NPU 预聚合 | `monitoring/config-for-infra-cn4-x86-common-cluster/prometheus-npu-rules.yaml` |
| 看板 | `monitoring/grafana/dashboards/` |
| ArgoCD 应用 | `argocd/clusters/<cluster>/` |
