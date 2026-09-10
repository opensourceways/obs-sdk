# community 取值枚举

> 权威来源：`opensourceways/infrastructure` 仓 [`service.yaml`](https://github.com/opensourceways/infrastructure/blob/main/service.yaml)
> 顶层的 `communities` 列表。service.yaml 新增/改名社区时，本文件随服务接入同步更新。
> `community` 字段/label 取值**必须落在此枚举内**（log/label 里的取值统一转小写，见下）。

## 取值（跟随 service.yaml 的 communities 列表）

`Ascend` · `BoostKit` · `CANN` · `Common` · `HiFloat` · `HPCKit` · `Infrastructure` · `Merlin` · `MindSpore` · `openEuler` · `OpenFuyao` · `openGauss` · `OpenJiuwen` · `openLookeng` · `OpenPangu` · `OpenUBMC` · `UnifiedBus` · `Xihe`

> 上表大小写跟随 service.yaml 原样。**通用约定：log/label 里统一转小写**以保跨服务可聚合（查询聚合大小写敏感）。例：service.yaml `Ascend` → 字段值 `ascend`；`openEuler` → `openeuler`；`MindSpore` → `mindspore`。

### 如何重新同步

service.yaml 有上万行，社区名在 `communities:` 段内以零缩进的 `- name:` 出现（其下 `services:` 里缩进两级的 `- name:` 是**服务名**，别取错）。重新提取：

```bash
gh api repos/opensourceways/infrastructure/contents/service.yaml \
  -H "Accept: application/vnd.github.raw" \
  | awk '/^communities:/{f=1;next} /^[a-z_]+:/{f=0} f&&/^- name: /{sub(/^- name: /,"");print}'
```

## 多社区 / 中心化部署

- 单社区独立部署：`OBS_COMMUNITY=<小写值>`。
- 中心化单实例多社区：进程级无单一 community，请求级覆盖值为上表枚举内小写值之一。
- 默认/未知：`unknown`（保留值，表示尚未判定归属，不应出现在正常运营数据里）。
