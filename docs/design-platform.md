# netops 平台能力设计（对标商用产品）

## 1. 目标

netops 已经提供运维内核：设备与清单、采集与快照、配置解析与比对、规则引擎、变更计划与执行、治理与分波。fabric、wireless、firemon、net-connector 都依赖它。

对照商用网络自动化与 NCCM 产品，内核还缺"平台级"能力。这些能力目前要么各工具各写一份，要么根本没有：

| 商用产品 | 代表能力 | netops 现状 |
|---|---|---|
| Cisco NSO | 设备锁、check-sync（带外变更检测）、dry-run、commit queue、回滚文件 | 有 dry-run 和回滚；没有跨工具设备锁，也没有只读的同步检测 |
| Juniper Apstra / Forward / IP Fabric | 快照比对、异常告警、基于意图的校验 | 有快照、比对和规则引擎；没有统一事件与告警 |
| SolarWinds NCM / ManageEngine NCM / Oxidized | 定时备份、变更通知、软件版本合规（EOL/EOS、目标版本）、报表导出 | 备份和比对已有；没有调度、通知、版本合规和统一报表 |
| Ansible AWX / Itential | 作业模板、调度、运行历史、RBAC、审批 | 审批已有；没有作业调度和运行历史，也没有权限模型 |
| NetBox | 角色、站点、标签、变更日志 | 设备模型和审计已有 |

本设计补齐下列能力，并要求它们对四个消费模块都适用：

| 能力 | 包 | 层 | 典型使用方 |
|---|---|---|---|
| 运维事件与告警 | `event`（模型、总线）、`notify`（文件、Webhook 投递） | 领域 / 边缘 | 全部：变更结果、带外变更、合规违规、采集失败 |
| 作业调度与运行历史 | `job`（调度表达式、调度器、历史端口）、`store/runs` | 领域 / 边缘 | wireless watch、定时备份、firemon 命中采集 |
| 跨工具设备租约 | `lease`（租约模型、管理器）、`store/lease` | 领域 / 边缘 | fabric 部署、wireless 处置、firemon 下发互斥 |
| 软件生命周期合规 | `lifecycle` | 领域 | 全部：目标版本、最低版本、禁用版本、EOS/EOL |
| 权限模型 | `authz` | 领域 | 全部 CLI：采集、规划、审批、下发、回滚的授权 |
| 统一报表 | `report` | 领域 | fabric、firemon、wireless 的报告导出（文本、Markdown、CSV、JSON） |
| 批量执行增强 | `batch` | 领域 | 只读任务的有界重试、按站点的并发上限 |
| 带外变更检测 | `ops.CheckSync` | 用例 | 全部：运行配置与版本库最新版本比对 |

## 2. 设计原则

- **领域与 I/O 分离**：`event`、`job`、`lease`、`lifecycle`、`authz`、`report` 都不做文件或网络 I/O，持久化与投递经端口注入；文件实现放在 `store/*`，网络投递放在 `notify`。
- **不阻塞主流程**：事件发布失败不影响变更结果，但必须记入结果并可查询。只有租约和授权会主动拒绝操作。
- **默认安全**：
  - 授权没有配置时，显式选择"全部允许"才放行；
  - 租约冲突时报告持有者，不抢占；
  - 过期租约只有在超过 TTL 后才可接管。
- **确定性**：调度的下次时间、报表行序、事件序列化都是确定的；时钟一律注入。
- **秘密与原文**：事件、报表、运行历史只携带失败码、摘要和结构化字段，不携带设备原文或凭据。Webhook 签名密钥用 `secret.Secret`。

## 3. 模块抽象

### 3.1 event：运维事件

```go
type Kind string // "config.changed" "config.out_of_sync" "compliance.violation"
                 // "collect.failed" "change.executed" "change.uncertain"
                 // "lifecycle.violation" "lease.conflict" "job.failed" ...
type Event struct {
    ID       string            // 内容摘要派生，重复发布可去重
    Kind     Kind
    Severity assess.Severity
    At       time.Time
    Source   string            // 产生事件的工具，如 "fabric"
    Device   string            // 设备 ID，可空
    Subject  string            // 对象路径，如 "interface:Eth1/1"
    Summary  string            // 面向运维的一句话，不含设备原文
    Fields   []evidence.Field
    Refs     []evidence.SourceRef
}
type Sink interface{ Deliver(ctx, []Event) error }
type Bus  // 构造时注入 Sink 与 Filter；Publish 按过滤条件扇出，汇总投递失败
type Filter struct{ Kinds []Kind; MinSeverity assess.Severity; Devices device.Selector }
```

- 事件 ID 由 Kind、Device、Subject、At、Summary 派生，接收方可以据此幂等去重。
- `Bus.Publish` 逐个 Sink 投递，单个 Sink 失败不影响其他 Sink，返回的错误合并了全部失败。
- `notify.File` 追加 JSONL（私有文件）；`notify.Webhook` POST JSON，带 HMAC-SHA256 签名头，有超时，并对 5xx 和网络错误做有界重试。

### 3.2 job：作业调度

```go
type Spec      // 解析自 "@every 5m"、"@hourly"、"@daily"、5 段 cron "0 2 * * 1-5"；时区显式
func ParseSpec(expr string, loc *time.Location) (Spec, error)
func (Spec) Next(after time.Time) time.Time
type Job struct{ Name string; Spec Spec; Run func(ctx) (Outcome, error); Timeout time.Duration }
type Run struct{ Job, Started, Finished, Status(succeeded|failed|skipped), Codes []failure.Code, Summary string }
type History interface{ Record(ctx, Run) error; Last(ctx, job string, n int) ([]Run, error) }
type Scheduler // Run(ctx) 阻塞直到取消；同一作业不重叠（上次未结束则记 skipped）；时钟注入
```

- cron 支持 `*`、列表、范围、步长；DOM 与 DOW 同时受限时按 cron 语义取并集。
- 调度器不补跑错过的时间点，只从当前时间算下一次，避免进程恢复后集中执行。
- `store/runs` 为每个作业保留最近 N 次运行（JSON）。

### 3.3 lease：跨工具设备租约

```go
type Holder struct{ Tool, Actor, Purpose string }   // Purpose 如 plan ID
type Lease  struct{ Device string; Holder Holder; Acquired, Expires time.Time; Token string }
type Store interface{ // 原子的比较并交换
    Get(ctx, device) (Lease, bool, error)
    Put(ctx, Lease, expectToken string) error          // 令牌不符返回 ErrConflict
    Delete(ctx, device, token string) error
}
type Manager // Acquire(ctx, devices, holder, ttl) 全部成功或全部回退；Renew；Release
var ErrHeld  // 包含当前持有者，供 CLI 提示"被 fabric/alice 的 chg-12 占用至 …"
```

- 租约针对设备，不针对会话：同一工具、同一 Purpose 重入时续期，不同持有者一律冲突。
- `store/lease` 用文件锁保证比较并交换，一台设备一个 JSON 文件。
- `ops.DeployRequest` 新增可选 `Leases`：先对一个波次的设备整体获取租约，波次结束后释放。

### 3.4 lifecycle：软件生命周期合规

```go
type Version // 解析 NX-OS "9.3(10)"、IOS-XE "17.9.4a"、Comware "7.1.070, Release 6728P05"、
             // Junos "21.4R3-S5"：按数字段和字母段逐段比较
func ParseVersion(s string) Version
func (Version) Compare(Version) int
type Rule struct{ Name string; Match device.Selector; Target, Minimum string; Blocked []string;
                  EndOfSale, EndOfSupport time.Time; Advisory string }
type Policy struct{ Rules []Rule }                    // 首个匹配规则生效
type Status string // compliant | below_minimum | not_target | blocked | end_of_support | unknown
type Verdict struct{ Device; Rule string; Version string; Status; Statuses []Status; Detail string }
func (Policy) Evaluate(d device.Device, version string, now time.Time) Verdict
func (Verdict) Finding() assess.Finding
```

- 规则用 YAML 描述，按 `device.Selector` 匹配平台、型号、角色、站点。
- 一个设备可以同时有多个问题（例如低于最低版本且已过 EOS），`Statuses` 全部列出；`Status` 取其中最严重的一个。

### 3.5 authz：权限

```go
type Permission string // view collect backup plan approve deploy rollback admin
type Role struct{ Name string; Permissions []Permission; Scope []string } // Scope 为设备选择器
type Policy struct{ Roles []Role; Bindings map[string][]string; AllowAll bool }
func (Policy) Authorize(actor string, p Permission, d device.Device) error // ErrDenied 带原因
func (Policy) Permits(actor string, p Permission) bool                     // 不涉设备的操作
```

- `admin` 隐含全部权限；Scope 为空表示全部设备。
- `ops.DeployRequest` 新增可选 `Authorize func(device.Device) error`，在打开会话前拒绝越权设备；`governance` 审批只计入拥有 `approve` 权限的审批人，由调用方先用 Policy 过滤。

### 3.6 report：统一报表

```go
type Document struct{ Title string; Generated time.Time; Summary []KV; Sections []Section }
type Section  struct{ Title string; Text []string; Table *Table; Findings []assess.Finding }
type Table    struct{ Columns []string; Rows [][]string }
func Text(w, Document) error; Markdown(w, Document) error; CSV(w, *Table) error; JSON(w, Document) error
func Findings(fs []assess.Finding) *Table // 发现统一成表：严重级别、规则、对象、说明
```

- 渲染器只写 `io.Writer`；文本渲染按东亚字宽对齐，所有单元格都转义控制字符。
- Markdown 会转义表格中的 `|` 与换行；CSV 遵循 RFC 4180。

### 3.7 batch 增强

- `Options.Retry`：`{Attempts, Backoff, Retryable func([]failure.Code) bool}`。只用于只读任务：默认只重试超时、不可达、会话忙；退避尊重 ctx；结果记录 `Attempts`。
- `Options.Partition` 与 `Options.PartitionLimit`：按分区键（如站点）限制并发，防止压垮一个站点的广域链路或同一控制器集群。

### 3.8 ops.CheckSync：带外变更检测

```go
type SyncStatus string // in_sync | out_of_sync | no_baseline | failed
type CheckSyncRequest struct{ Devices; Command func(d) string; Dialect func(d) config.Dialect;
                              Dialer; Versions VersionSource; Events event.Publisher; ... }
type Synced struct{ Device; Status; Latest *config.Version; Observed evidence.Digest; Added, Removed int }
```

- 读取运行配置、规范化后与版本库最新版本比较，不写版本库（与 Backup 的区别）。
- 不一致时发布 `config.out_of_sync` 事件，并附加行数统计，不附加配置原文。

## 4. 与现有包的关系

```
ops ──▶ event（发布） lease（获取） authz（判定） batch（重试/分区）
notify ──▶ event      store/runs ──▶ job      store/lease ──▶ lease      report ──▶ assess
lifecycle ──▶ device assess        job ──▶ failure        event ──▶ assess device evidence
```

## 5. 实施顺序与验收

1. **基础**：`event`、`report`、`lifecycle`、`authz`。纯领域，表驱动测试和 Example 都要有。
2. **调度与租约**：`job` 及 `store/runs`，`lease` 及 `store/lease`，并发测试使用 `-race`。
3. **投递**：`notify` 的文件与 Webhook。Webhook 用 `httptest` 验证签名、重试与超时。
4. **集成**：
   - `batch` 增加重试与分区；
   - `ops.Deploy` 接入租约、授权、事件；
   - `ops.Backup` 与 `ops.Collect` 发布事件；
   - 新增 `ops.CheckSync`。
5. **收尾**：更新 `architecture.yaml`、README、CHANGELOG、GLOSSARY，`make check` 全部通过。消费模块按需接入，不强制。
