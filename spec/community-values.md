# community 取值枚举

> 权威来源：`opensourceways/infra-common` 仓 [`service.md`](https://github.com/opensourceways/infra-common/blob/master/service.md) 的**社区段**（section）。service.md 新增/改名社区时，本文件随服务接入同步更新。
> `community` 字段/label 取值**必须落在此枚举内**（小写用连字符的取值为部署时约定别名，见下）。

## 取值（跟随 service.md 常用社区段）

`Ascend` · `BoostKit` · `CANN` · `HPCKit` · `MindSpore` · `openEuler` · `OpenFuyao` · `openGauss` · `OpenJiuwen` · `OpenUBMC` · `OpenPangu` · `HiFloat` · `UnifiedBus` · `Infrastructure` · `openLookeng` · `Xihe`

> 上表大小写跟随 service.md 原样。**通用约定：log/label 里统一转小写**以保跨服务可聚合（查询聚合大小写敏感）。例：service.md `Ascend` → 字段值 `ascend`；`openEuler` → `openeuler`；`MindSpore` → `mindspore`。

## 多社区 / 中心化部署

- 单社区独立部署：`OBS_COMMUNITY=<小写值>`。
- 中心化单实例多社区：进程级无单一 community，请求级覆盖值为上表枚举内小写值之一。
- 默认/未知：`unknown`（保留值，表示尚未判定归属，不应出现在正常运营数据里）。
