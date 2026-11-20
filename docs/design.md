# netops 设计

## 目标

为网络运维工具提供一套厂商无关的内核，使每个工具只需实现三样东西：领域模型、厂商语法（命令表与解析器）、规则目录。其余的设备清单、采集、缓存、配置管理、变更管理与工具装配都由 netops 提供，且语义一致、可审计。

## 分层

```
cmd → internal/cli → internal/bootstrap ──装配──▶ net-connector connector / store/* / inventory / credential / bootstrap / term   （边缘，I/O）
                     internal/workflow ──▶ ops（用例，只依赖端口）
                                            │
         rollout  governance  apply  batch  cache  change  health  baseline  assess  drift  diff  config  capture （领域）
                                            │
                          device  evidence  parse  failure  secret                          （基础）
```

- 基础与领域包不做 I/O；`ops` 只依赖端口接口（`Dialer`、`Session`、`Snapshots`、`Archive`、`Probes`、`apply.Journal`）。
- netops 不导入任何传输模块：设备会话由 net-connector `connector.Dialer.Port()` 实现 `ops.Dialer`，在工具的组合根注入；平台识别经 `inventory.Classifier` 端口由 net-connector 注册表实现。
- 规则由 `architecture.yaml` 强制。

## 核心模型

| 模型 | 不变量 |
|---|---|
| `device.Device` / `Inventory` | ID 与管理地址唯一；构造时校验，被拒记录带原因计入 `Report`；按地址排序。 |
| `evidence.Observation[T]` / `Coverage` | 失败、不支持、未请求的观测不带事实；完整的观测不带问题；覆盖度不可变。 |
| `capture.Capture` | 保存原始回显；`Body` 是去除回显与提示符的唯一实现，行号对应原文；`Merge` 不让失败覆盖已有好结果。 |
| `config.Document` | 解析一次；规范化文本去掉注释、分隔符、终止行、易变行，`Digest` 只随实质改动变化；`Masked` 遮蔽秘密。 |
| `change.Plan[M]` | 不可变；`Precheck` 区分 ready / satisfied / drifted；`Verify` 要求变更达成且（`Preserving` 时）无旁带改动；`Inverse` 生成回滚计划；信封摘要防篡改。 |
| `apply.Record` | 只含失败码与脱敏命令；`Sent()` 一旦为真，任何失败都是 uncertain。 |
| `assess.Engine[M]` | 参数在构造时按声明类型校验；证据不全跳过；规则 panic 只使该规则失败。 |

## 运维能力

### 设备清单
静态 YAML（带 defaults）与 Netdisco（HTTP/PostgreSQL，原生实现，带按身份隔离的私有缓存）按顺序合并，后者覆盖前者同 ID/同地址的记录。`device.ParseSelector` 是所有工具共享的选择语法。

### 配置解析
`parse.Content` 有界索引行并计算摘要；`Cursor` 记录已解析行，`Scanner` 把未解析行转为 `evidence.Issue`，保证"看不懂的行"不会被静默忽略。`Columns`（表头或标尺）处理定宽表，`Tree` 处理缩进层级，`config.Dialect` 处理 Comware 的 `#` 分隔与 Cisco 的 `!` 注释。

### 批量采集与缓存
`batch.Run` 是唯一的并发入口：有界并发、单设备超时、错误分类、结果按设备排序、每台结果立即持久化。`ops.Collect` 每台设备一个会话，部分采集也会合并入快照并记录刷新状态；`ops.Refresh` 用 `cache.Policy` 判定过期维度，按维度集合分组刷新，失败退避期内不再连接，返回 `Freshness` 供界面展示数据新旧。

### 配置管理
`ops.Backup` 采集运行配置、按方言规范化后写入 `store/archive`：规范化摘要不变则只更新"最后检查时间"，变化则按原文摘要存储对象并追加版本。`At`/`Seq`/`Latest` 支持按时间点或版本号取配置，`Prune` 执行"保留最近 N 份 + 最近 N 天每日一份"并回收无引用对象。`ops.Compare` 同时给出结构化块差异与遮蔽秘密的统一 diff；`ops.Compliance` 用分层基线检查必需与禁止命令，结果是标准 `assess.Finding`。

### 变更管理与批量下发
1. 规划：领域模块实现 `change.Change[M]`，`change.NewPlan` 生成计划，`Envelope` 存入 `store/journal` 供评审。
2. 审批：`governance.Approval` 绑定变更集摘要（`ops.ChangeSet`），不能自审，可设有效期。
3. 门禁：`governance.Gate` 一次性列出全部拒绝原因（窗口、封网、单号、审批、设备数上限）；紧急变更须填写理由，绕过记录在决定中。
4. 分波：`rollout.Schedule` 先金丝雀，再按层级与分区成波，反亲和组不同波；`Tracker` 按失败预算停止。
5. 单设备流水线（一个会话）：健康快照 → 可选检查点 → `apply.Run`（复核、意图持久化、逐条下发、回读验证）→ 健康快照与门禁 → 门禁失败时经检查点或反向计划回滚 → 通过则保存。
6. 默认 `apply.ModeDryRun` 可演练；所有状态写入 journal 与只追加审计。

### 拓扑与接口描述
`neighbor` 以 TextFSM 模板解析各平台 LLDP/CDP 输出，并用独立计数核对行数，解析不全即报错；`iface` 统一接口名的比较键、缩写与配置全名；`describe` 用 `Profile` 采集邻居与运行配置，`Policy.Audit` 给出逐端口结论（证据不足的端口只报告不修改），`Audit.Change` 生成 `change.Change`，经 `ops.Deploy` 复核、下发、回读、可回滚。

### 设备事实
`facts.Probe` 给出各平台只读命令与 TextFSM 模板；没有软件版本的输出一律视为无法识别，避免错误页被当成空事实。`facts.Compare` 列出清单记录（名称、型号、版本）与设备自报不一致之处。

## 与旧实现的对应

| 旧位置 | 新位置 |
|---|---|
| go-netops `evidence` `assess` `drift` `change` `apply` `batch` `store` `session` `inventory` `bootstrap` | `evidence` `assess` `drift` `change` `apply` `batch` `store/*` `failure` `inventory` `bootstrap` |
| parser、go-netops `textscan` | `parse` |
| wireless/fabric `adapter/snapshot` | `store/snapshot` |
| wireless/fabric `adapter/netconnector`、netops `connect` | net-connector `connector`（实现 `ops` 端口） |
| net-connector `topology`、`internal/ifname`、拓扑 TextFSM 模板 | `neighbor` `iface` `describe` |
| net-connector `netdisco`、`inventory`、`fleet` 规则 | `inventory`（Netdisco、选择规则、`Classifier` 端口） |
| net-connector `storage` | `store/disk` |
| net-connector `facts`、`vendors/internal/driver.FactsReader` | `facts` |
| wireless `internal/workflow` collect/refresh/apply/load | `ops.Collect` `ops.Refresh` `ops.Deploy` |
| fabric `internal/parallel`、net-connector `parallel`、各处信号量 | `batch`（errgroup） |
| fabric `governance` `rollout` `status` 通用部分 | `governance` `rollout` `health` |
| fabric `vendors/nxos` columns/tree | `parse.Columns` `parse.Tree` |
| wireless/fabric `internal/bootstrap` 配置、凭据、选择器 | `bootstrap` `credential` `device.Selector` |
| wireless/fabric `internal/cli` 表格、转义、确认、JSON、退出码 | `term` |

## 尚未覆盖

- 只有 Cisco 与 Comware 两种方言；Junos 的花括号与 set 格式需要另写方言。
- 配置版本库只有文件系统后端；`ops.Archive` 端口预留了数据库实现。
- 在线采集与下发命令必须在实验室设备上验证，单元测试不代表真实设备行为。
