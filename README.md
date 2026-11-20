# netops

网络运维工具的厂商无关内核：设备清单、配置解析、批量采集与缓存、配置备份与比对、合规检查、受控变更（规划、审批、分波下发、健康门禁、回滚）。

`fabric`（数据中心）与 `wireless`（无线控制器）等工具在其上实现领域模型、厂商语法和规则目录。设备交互由 [net-connector](https://github.com/gatework/net-connector) 实现 `ops` 端口，netops 本身不依赖它。

## 包

| 能力 | 包 | 说明 |
|---|---|---|
| 基础 | `secret` `failure` `evidence` | 不可打印的秘密；稳定失败码；证据状态、来源、覆盖度、显式字段指纹 |
| 设备清单 | `device` `inventory` | 统一设备身份、去重校验的清单、选择器语法；静态 YAML 与 Netdisco 来源 |
| 配置解析 | `parse` `config` | 行索引、未解析行记账、字段/定宽表/缩进树；配置方言、规范化与摘要、秘密遮蔽 |
| 批量采集 | `capture` `batch` `store/snapshot` | 原始回显模型；有界并发与完整账目；快照目录 |
| 拓扑与描述 | `neighbor` `iface` `describe` | LLDP/CDP 邻居解析；接口名规范化；描述审计与可回滚变更 |
| 设备事实 | `facts` | 型号、序列号、版本、运行时间解析（7 个平台），与清单记录比对 |
| 缓存管理 | `cache` `ops.Refresh` | 按维度 TTL、离线/实时模式、失败退避、按维度分组刷新 |
| 差异比对 | `diff` `drift` | 有界行级 diff 与统一格式、配置块结构 diff；语义偏差与共识 |
| 配置管理 | `store/archive` `baseline` `ops.Backup` `ops.Compare` `ops.Compliance` | 内容寻址版本库；分层基线与文本合规 |
| 巡检 | `assess` `health` | 规则引擎与类型化参数；健康快照、门禁与评级 |
| 变更管理 | `change` `governance` `rollout` `store/journal` | 不可变计划与防篡改信封；窗口、封网、审批；分波与反亲和；审计 |
| 批量下发 | `apply` `ops.Deploy` `ops.Rollback` | 单设备执行状态机；治理→分波→单会话（健康、检查点、执行、门禁、回滚、保存） |
| 工具装配 | `credential` `bootstrap` `term` | 凭据提供者；配置发现与 .env；退出码、表格、确认、JSON、中文文案 |

## 最小用法

```go
plan, err := change.NewPlan(meta, dev, running, myChange, fingerprint)
rep, err := ops.Deploy(ctx, ops.DeployRequest[Model]{
	Plans: []*change.Plan[Model]{plan}, Dialer: dialer.Port(), Read: readModel,
	Journal: func(p *change.Plan[Model]) apply.Journal { return j.Execution(p.Meta().ID, p.Target().ID) },
	Strategy: rollout.Strategy{Canary: 1}, Save: true,
})
```

每个公开包都有可运行的 `Example*` 测试。

## 测试

- `make check`：格式、vet、lint、架构、`go test -race`、漏洞扫描。单元测试不连网、不连设备；工作流测试使用 `internal/fake` 的内存设备。
- `make fuzz`：解析器、方言、diff、选择器、计划信封与策略解码的模糊测试。
- 真实设备验证在消费模块中显式启用（如 `wireless` 的 `make corpus`）。

## 文档

[docs/design.md](docs/design.md) · [docs/adr/0001-netops-kernel.md](docs/adr/0001-netops-kernel.md) · [GLOSSARY.md](GLOSSARY.md) · [CHANGELOG.md](CHANGELOG.md)
