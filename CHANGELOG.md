# Changelog

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本遵循语义化版本。

## [Unreleased]

### Added

- `bootstrap.Env.SharedDir` 与 `bootstrap.LeaseArea`：所有工具共享的数据区（默认 `~/.local/share/gatework/<name>`，可用 `$GATEWORK_<NAME>_DIR` 覆盖），设备租约必须放在这里才能跨工具互斥。

## [0.5.0] - 2026-11-22

### Added

- 平台能力，对照商用 NCCM 与网络自动化产品补齐（[docs/design-platform.md](docs/design-platform.md)）：
  - `event`：运维事件模型（内容派生 ID）、过滤路由与总线；
  - `notify`：JSONL 文件、HMAC-SHA256 签名且有界重试的 Webhook、终端投递；
  - `job`：调度表达式（`@every`、`@hourly`、`@daily`、`@weekly`、5 段 cron）与不重叠的调度器；`store/runs` 保存运行历史；
  - `lease`：跨工具设备租约，全部成功或全部回退，TTL 过期接管；`store/lease` 为文件实现；
  - `lifecycle`：多厂商版本比较，以及目标、最低、禁用版本与 EOS/EOL 策略；
  - `authz`：角色、权限与设备作用域；
  - `report`：统一报表与文本、Markdown、CSV、JSON 渲染。
- `batch.Options.Retry`（只读任务的有界重试，默认只重试瞬时失败）与 `Partition`/`PartitionLimit`（按分区限流）；`batch.Result.Attempts`。
- `ops.Deploy`：
  - `Authorize`：任何设备越权即整体拒绝（`ErrUnauthorized`，`DeployReport.Denied`）；
  - `Leases`/`Holder`/`LeaseTTL`：按波次租约；
  - `Notify`：发布变更事件。
- `ops.Collect` 与 `ops.Backup` 增加 `Pacing` 与 `Notify`（`collect.failed`、`config.changed`）。
- `ops.CheckSync`：只读的带外变更检测（`in_sync`、`out_of_sync`、`no_baseline`）与 `ops.Versions` 端口。

### Changed

- `store/archive.ErrNotFound` 现在就是 `config.ErrNotArchived`，只依赖端口的调用方也能识别"未归档"。`errors.Is` 的判断结果不变，只是错误文本变了。

## [0.4.0] - 2026-11-21

### Added

- `ops.ConfigReader` 为会话提供可选的运行配置读取能力。备份优先使用会话自身的读取方式，并保留对通用命令会话的支持。

### Changed

- 更新 `glob`、`flock` 和 `renameio/v2`，继续支持 Go 1.26。

## [0.3.0] - 2026-11-20

### Changed

- `bootstrap`：统一 gatework 工具目录布局。配置发现改为 `$<APP>_CONFIG` → `./<app>.yml` → `~/.gatework/<app>.yml`（不再查找 `$XDG_CONFIG_HOME/<app>/` 与 `~/.config/<app>/`）；`.env` 随之默认为 `~/.gatework/.env`。新增 `Env.ConfigPath`、`Env.DataDir`（`~/.local/share/gatework/<app>`，不随 `$XDG_DATA_HOME` 变化）与常量 `ConfigHome`、`DataHome`。

## [0.2.0] - 2026-11-20

- 新增 `netops/set`，完整提供原 `gatework/set v0.2.0` 的整数闭区间 API。

## [0.1.0] - 2026-11-20

### Added

- 首个版本：替代 go-netops v0.4.2 与 parser v0.2.2 的运维内核（ADR 0001）。
- 基础：`secret`、`failure`（稳定失败码）、`evidence`（不可变覆盖度、显式字段指纹）、`device`（统一身份、清单、选择器）。
- 解析：`parse`（并入 parser 的 Content/Cursor 与 textscan，新增定宽列与缩进树）、`config`（Cisco 与 Comware 方言、规范化摘要、秘密遮蔽）。
- 采集与缓存：`capture`、`batch`、`cache`、`store/snapshot`（兼容 net-connector 批量目录与旧 wireless/fabric 缓存）。
- 比对与配置管理：`diff`（有界行级 diff、统一格式、配置块结构 diff）、`drift`、`baseline`、`store/archive`（内容寻址版本库与保留策略）。
- 巡检：`assess`（类型化参数、可并行、可取消、多策略共享）、`health`（门禁与评级）。
- 变更：`change`（幂等复核、反向计划、防篡改信封、显式编解码表）、`governance`（窗口、封网、审批、紧急变更）、`rollout`（金丝雀、分层、分区、反亲和、失败预算）、`apply`（单会话执行状态机、命令脱敏、有界持久化、dry-run）、`store/journal`（计划、审批、执行、审计）。
- 迁移 wireless 时补充：`change.Effect`（相对前态验证的命令式变更，如踢下线）、`ops.DeployRequest.Read` 接收计划（读回可依赖计划）、`ops.CollectRequest.OnCapture`（采集后派生记录，如终端位置）；完整观测允许携带说明性问题（如容忍的计数漂移）。
- 拓扑与设备事实：接口名规范化、LLDP/CDP 邻居解析、接口描述审计、设备信息与清单比对。
- 装配：`credential`、`inventory`（静态 YAML、Netdisco）、`bootstrap`、`term`、`ops`（Collect、Refresh、Backup、Compare、Compliance、Deploy、Rollback）。
