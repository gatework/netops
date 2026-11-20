# netops 开发说明

遵循 [gatework Go 工程规范（go-standards/STANDARD.md）](https://github.com/gatework/go-standards/blob/v1.2.0/STANDARD.md)；提交前运行 `make check`。

netops 是 fabric、wireless 等运维工具共用的领域内核，替代已冻结的 go-netops 与 parser（见 [docs/adr/0001-netops-kernel.md](docs/adr/0001-netops-kernel.md)）。设计见 [docs/design.md](docs/design.md)，术语见 [GLOSSARY.md](GLOSSARY.md)。

## 边界

- 厂商无关、领域无关：不得导入 fabric、wireless、go-vxlan、go-wireless、go-netops、parser 或任何厂商语义。`config` 只描述语法方言（注释、分隔符、易变行、敏感行），不解释命令含义。
- 领域包（secret、failure、evidence、device、parse、capture、config、diff、drift、assess、baseline、health、change、governance、rollout、apply、batch、cache）以及 iface、neighbor、facts、describe 与用例包 `ops` 不做 I/O。
- netops 不导入 net-connector、go-expect 或任何消费工具；设备会话经 `ops.Dialer`/`ops.Session` 端口由 net-connector 实现并在工具组合根注入。`ops` 只依赖端口。分层由 `architecture.yaml` 约束，新增包须先登记。
- 不引入 `init` 注册或包级可变状态；目录、编解码表、策略都显式构造。唯一例外是 `bootstrap` 中保护进程级 RLIMIT 的互斥锁。

## 不可破坏的语义

- 证据不完整的规则跳过（`insufficient_evidence`），不得给出结论；健康门禁在证据不完整时判为不通过。
- `apply`：触碰设备前持久化 planned；每条命令发送前持久化意图；一旦可能已下发，任何失败都是 `uncertain`；不重试、不重放、不自动回滚。会话关闭失败使已验证的结果变为 `uncertain`（`apply.Settle`）。
- 变更计划不可变；执行前重新读取设备：已满足则跳过（幂等），被他人改动则拒绝（`drifted`）。执行的是审批时的脚本，不重新渲染。
- 失败只以 `failure.Code` 记录；错误文本、日志、报告、journal 中不得出现设备原文、凭据或明文秘密。凭据只以 `secret.Secret` 传递。
- 快照与配置版本库的写入都是原子替换，读取都校验大小与摘要。

## 构建与检查

```sh
make check   # 提交前必须通过
make fuzz    # 修改 parse、config、diff、device 选择器、change 编解码或 assess 策略时运行
```

依赖使用 `go.mod` 固定的正式版本，不提交 `go.work` 或持久化 `replace`。发布 tag 属于对外操作，需明确授权。
