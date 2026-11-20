# 0001 用 netops 取代 go-netops 与 parser

日期：2026-11-19 · 状态：已采纳

## 背景

fabric 与 wireless 都依赖 go-netops v0.4.2 与 parser v0.2.2，但各自重写了快照目录、net-connector 会话适配、凭据与 SSH 设置、并发池、CLI 工具与设备身份，且实现已经分叉。go-netops 本身存在接口错位（`change.Plan` 不满足 `apply.Plan`）、回滚只渲染不执行、无幂等与 dry-run、batch 丢弃错误原因、store 只写不读、包级可变缓存、`any` 承载领域数据、凭据不脱敏等问题；parser 质量好，但没有被 textscan 与厂商解析器使用。

## 决策

- 新建模块 `github.com/gatework/netops`，冻结 go-netops 与 parser，只作参考，不提供兼容层。
- parser 的 Content/Cursor 与 go-netops textscan 合并为 `netops/parse`。
- 共享机制（快照、会话、凭据、并发、缓存策略、变更执行、CLI 工具）全部下沉到 netops；领域模型、厂商语法与规则目录留在消费模块。
- 配置版本库使用文件系统内容寻址，经 `Archive` 端口预留其他后端。
- 迁移顺序：先 wireless（本轮），再 fabric。

## 后果

- 消费模块删除各自的 adapter/snapshot、adapter/netconnector 与 workflow 通用逻辑，改为装配 netops 的适配器。
- 快照目录读取兼容 net-connector 批量布局与旧缓存，已有缓存与私有语料无需转换。
- 在 netops 发布 tag 之前，消费模块只能用未提交的临时 go.work 联调（MOD-2、MOD-3）。
