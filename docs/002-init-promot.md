# Codex 执行提示词：实现 Piglet

下面给出一份主提示词，以及建议按阶段投喂的分段提示词。

使用方式：

1. 将 `piglet-prd.md` 放入目标仓库的 `docs/PRD.md`；
2. 在新仓库 `pgsty/piglet` 根目录启动 Codex；
3. 首先使用“主提示词”；
4. 如果单次任务窗口有限，依次使用 M0/M1、M2、M3/M4 分段提示词；
5. 不要让 Codex跳过真实测试，也不要把未运行的 E2E 标记为通过。

---

# 一、主提示词

你正在 `pgsty/piglet` 仓库中工作。你的任务是依据 `docs/PRD.md`，实现一个可发布的 Go 项目 Piglet。

Piglet 是一个面向 Pigsty 的跨平台 QEMU 虚拟机运行器，用于替代 Pigsty 当前的 Vagrant + VirtualBox/libvirt 本地虚拟机体系。

请把 `docs/PRD.md` 视为需求真相。除非 PRD 内部矛盾，否则不要反复询问产品决策，也不要擅自扩展成通用 Vagrant、Lima 或 libvirt 替代品。

## 总体目标

实现以下架构：

```text
macOS arm64/amd64 -> piglet -> QEMU -> HVF
Linux amd64/arm64 -> piglet -> QEMU -> KVM
```

v1 必须支持：

- Linux guest；
- 原生架构；
- qcow2 base image 与 COW root overlay；
- 数据盘；
- cloud-init NoCloud CIDATA；
- QMP 生命周期；
- SSH；
- `user` 单节点网络；
- `private` 多节点网络；
- macOS socket_vmnet；
- Linux bridge + qemu-bridge-helper；
- Pigsty meta/full/minio/simu 等 profile；
- doctor、状态恢复、日志、测试和发布。

## 强制约束

1. 只使用 QEMU backend。
2. 不引入 Vagrant、VirtualBox、VMware、libvirt、Lima。
3. 不实现 provider/plugin framework。
4. 不支持 Windows host。
5. 不支持非 Linux guest。
6. 不把跨架构 TCG 当作正式支持；非原生架构必须明确拒绝。
7. QEMU 进程不得以 root 运行。
8. 所有远程镜像必须校验 SHA-256。
9. 基础镜像只读，每个 VM 使用 qcow2 overlay。
10. 运行中的磁盘禁止调用 qemu-img 修改。
11. cloud-init seed 必须由 Go 原生生成，不依赖 genisoimage/xorriso/cloud-localds。
12. QMP 只实现最小稳定命令，不引入完整 QAPI code generation。
13. 外部命令必须使用 argv slice，禁止 shell 字符串拼接。
14. 所有状态写入必须 atomic。
15. 所有项目操作必须有排他锁。
16. destroy 必须验证路径边界，禁止误删项目数据目录之外的任何路径。
17. 不得把 mock 测试称为真实 KVM/HVF E2E。
18. 不得在没有运行测试的情况下宣称功能完成。
19. 不得为了赶进度跳过 error handling、rollback、日志和文档。
20. 不要实现 PRD 明确列为非目标的功能。

## 开始工作前

先完成以下动作，不要立即堆代码：

1. 阅读完整 `docs/PRD.md`。
2. 检查仓库现状、Go 版本、已有文件和 git 状态。
3. 如果相邻目录存在 Pigsty 仓库，阅读：
   - `vagrant/config`
   - `vagrant/Vagrantfile.libvirt`
   - `vagrant/Vagrantfile.virtualbox`
   - `vagrant/spec/meta.rb`
   - `vagrant/spec/full.rb`
   - `vagrant/spec/minio.rb`
   - `vagrant/spec/simu.rb`
4. 检查本机：
   - OS/arch；
   - qemu-system binary；
   - qemu-img；
   - accelerator；
   - firmware；
   - 是否具备真实 E2E 条件。
5. 创建并维护：
   - `TASKS.md`
   - `docs/ARCHITECTURE.md`
   - `docs/DECISIONS.md`
   - `docs/TESTING.md`
6. 把 PRD 拆成 M0、M1、M2、M3、M4 的可验证任务。
7. 标明当前环境无法执行的 E2E，但不要因此跳过对应实现与测试脚本。

## 实现顺序

严格按下面顺序推进。前一阶段测试未通过，不得开始后一阶段的大规模实现。

### M0：技术验证

实现最小可工作的：

- CLI；
- host/arch 探测；
- QEMU/qemu-img 探测；
- qcow2 overlay；
- Go 原生 CIDATA；
- user-mode NAT；
- QMP handshake/query-status/system_powerdown/quit；
- SSH；
- 单节点 start/stop。

必须在当前环境能运行的真实 accelerator 上完成 smoke；若当前机器不具备另一平台，则为另一平台写可执行 E2E 脚本并明确未验证。

### M1：Portable 单节点 MVP

实现：

- YAML config；
- JSON Schema；
- strict validation；
- image manifest；
- cache/checksum/atomic download；
- project UUID；
- XDG state；
- locks；
- root/data disks；
- dedicated SSH key；
- port forwards；
- lifecycle；
- drift hash；
- logs；
- doctor；
- debug bundle；
- unit/integration/golden tests；
- README 与安装文档。

M1 完成前，以下命令必须真实可用：

```bash
piglet init meta
piglet validate
piglet doctor
piglet up
piglet status
piglet ssh meta
piglet stop
piglet start
piglet logs meta
piglet destroy --force
```

### M2：Private 多节点

实现：

- dual NIC；
- cloud-init static private NIC；
- deterministic MAC；
- `/etc/hosts`；
- macOS socket_vmnet installer/status/uninstall；
- Linux piglet0 bridge installer/status/uninstall；
- qemu-bridge-helper；
- multi-node parallel start；
- partial failure；
- meta/full/minio profiles；
- private network E2E。

### M3：Pigsty 迁移

实现：

- profiles；
- `--image`；
- `--scale`；
- 当前主要 Vagrant spec 的 YAML 等价物；
- Pigsty Makefile 集成样例；
- legacy env wrapper；
- Vagrant 对照测试；
- image migration tooling/documentation。

### M4：GA

完成：

- guest matrix；
- Tier 2；
- packages；
- GoReleaser；
- Homebrew；
- RPM/DEB；
- self-hosted E2E；
- crash recovery；
- security review；
- troubleshooting；
- upgrade/state migration。

## 建议代码结构

以 PRD 为准，优先采用：

```text
cmd/piglet/
internal/cli/
internal/config/
internal/project/
internal/state/
internal/lock/
internal/doctor/
internal/image/
internal/cloudinit/
internal/qemu/
internal/network/
internal/ssh/
internal/platform/
internal/version/
assets/
schemas/
profiles/
docs/
```

不要为了“抽象漂亮”引入无用接口。这里只有一个 hypervisor：QEMU。只对确实有多种实现的部分建立接口，例如 network backend、platform profile、image source。

## 建议依赖

优先使用小而稳定的依赖：

- Cobra；
- `gopkg.in/yaml.v3`；
- `go-diskfs` 或经测试可正确生成 cloud-init CIDATA 的纯 Go ISO writer；
- `golang.org/x/sys`；
- Linux network install 如有必要使用成熟 netlink 库。

QMP 建议自行实现一个最小 client，因为只需要：

```text
qmp_capabilities
query-version
query-status
system_powerdown
quit
```

不要引入完整、版本滞后的 generated QAPI binding。

## 实现细则

### 配置

- 未知 YAML 字段必须报错；
- 所有 size/duration 使用有类型解析；
- canonical serialization 用于 spec hash；
- config diff 必须字段级输出；
- JSON Schema 与 Go validation 必须有一致性测试。

### 镜像

- 下载到 `.partial`；
- checksum 后 rename；
- base 只读；
- `qemu-img info --output=json`；
- backing path 绝对；
- state 记录 digest；
- 不自动更新已创建 VM 的 image。

### CIDATA

CIDATA 必须至少包含：

```text
user-data
meta-data
network-config
```

volume label：

```text
cidata
```

写 integration test，使用外部 `isoinfo` 仅作为 CI 可选验证；核心测试必须能用 Go 自己读取并验证内容。

### QEMU

- argv builder 纯函数化；
- 四个平台 profile；
- golden tests；
- 不使用 `-hda` 等 legacy shortcut；
- QMP Unix socket；
- serial/qemu log；
- PID；
- 普通用户；
- debug command 输出必须 shell-escaped，仅用于显示，执行仍使用 argv。

### 状态

- state version；
- atomic rename；
- fsync 必要目录；
- lock；
- stale PID/socket repair；
- QMP 优先判断；
- destructive path guard；
- partial create cleanup；
- existing data disk 永不因 reload/reconcile 被重建。

### SSH

- 项目专用 Ed25519 key；
- public key 注入全部节点；
- private key只注入 control 节点；
- SSH 调用系统 binary；
- 正确传递 terminal、stdin 和 exit code；
- 生成标准 ssh config。

### 网络

`user`：

- 只允许单节点；
- NAT；
- DHCP；
- hostfwd；
- 无 sudo。

`private`：

- 双网卡；
- 管理 NIC DHCP/default route；
- 私网 NIC static/no default route；
- macOS socket_vmnet；
- Linux bridge helper；
- QEMU 仍为普通用户；
- host/VM/VM 互通测试。

## 测试要求

每个功能至少具备以下一种或多种测试：

- unit；
- golden；
- integration；
- E2E。

必须运行：

```bash
go test ./...
go test -race ./...
go vet ./...
staticcheck ./...
```

对 platform-specific 代码使用 build tags，但公共语义必须通过跨平台编译：

```bash
GOOS=darwin GOARCH=arm64 go test ./...
GOOS=darwin GOARCH=amd64 go test ./...
GOOS=linux  GOARCH=amd64 go test ./...
GOOS=linux  GOARCH=arm64 go test ./...
```

如测试中使用 exec 或网络，必须有 timeout，不能无限等待。

## 工作方式

- 小步实现；
- 每完成一个子模块先补测试；
- 不修改无关文件；
- 不隐藏失败；
- 不用 TODO 代替关键路径；
- 外部环境缺失时输出可复制执行的验证命令；
- 每个 milestone 结束更新 `TASKS.md`；
- 对偏离 PRD的必要决策写入 `docs/DECISIONS.md`；
- 遇到非阻塞歧义时选择最保守、最小范围的实现并记录；
- 只有真正阻塞且 PRD 无法推导时才询问用户。

## 每次阶段完成后的输出

请报告：

1. 已完成的 requirement ID；
2. 修改/新增文件；
3. 实际运行的测试与结果；
4. 真实 E2E 环境；
5. 未验证项目；
6. 已知风险；
7. 下一阶段任务；
8. 任何需要用户拍板的阻塞项。

不要只给总结；必须留下可运行代码、测试、文档和命令。

现在开始：先阅读 PRD、审计仓库与环境，生成 `TASKS.md` 和架构文档，然后实施 M0。不要停留在脚手架阶段，继续工作直到 M0 退出标准满足，或出现无法通过代码解决的明确外部阻塞。

---

# 二、M0 + M1 分段提示词

继续依据 `docs/PRD.md` 实现 Piglet。本轮只做 M0 和 M1，不要提前实现 private network。

目标是得到一个真正可用的跨平台单节点 portable VM manager。

必须完成：

1. `piglet init meta`
2. strict YAML config + schema
3. host/QEMU/accelerator/firmware doctor
4. image manifest/cache/SHA-256
5. qcow2 base + root overlay + data disk
6. Go 原生 CIDATA
7. QEMU user-mode NAT + stable SSH forward
8. QMP lifecycle
9. project state/lock/stale repair
10. dedicated SSH key
11. `up/start/stop/status/ssh/exec/logs/destroy`
12. config hash 和 recreate detection
13. unit/integration/golden
14. 当前 host 的真实 E2E
15. README、architecture、testing、troubleshooting

特别检查：

- data disk 在 stop/start/reload 后不能被重建；
- root overlay backing path 使用绝对路径；
- QEMU 不以 root 运行；
- 非原生架构直接拒绝；
- interrupted download 不污染 cache；
- destroy 不可能逃逸 project data root；
- stop 在 guest 已 halt 时仍能回收 QEMU；
- 多节点配置在 user mode 必须报明确错误；
- 所有 external exec 使用 argv；
- 真实 E2E 与 mock 明确区分。

不要停在“代码能编译”。运行 M1 验收命令并修复所有发现的问题。

---

# 三、M2 分段提示词

继续依据 `docs/PRD.md` 实现 M2：private 多节点。

前提：M0/M1 已通过，不要重写 portable 路径。

必须完成：

1. 双网卡模型；
2. 管理 NIC 使用 user NAT 和 SSH forward；
3. private NIC 使用固定 MAC/IP，无默认路由；
4. `/etc/hosts` 注入全部节点；
5. macOS socket_vmnet：
   - pinned version；
   - checksum；
   - root-only install；
   - launchd；
   - status/uninstall；
   - QEMU stream netdev；
   - QEMU 非 root；
6. Linux：
   - piglet0；
   - 10.10.10.1/24；
   - qemu-bridge-helper；
   - bridge allowlist；
   - persistent install；
   - ownership marker；
   - safe uninstall；
7. 并行多节点；
8. 节点子集；
9. partial failure；
10. full/minio profiles；
11. host -> VM、VM -> VM、VM -> internet E2E；
12. 反复 create/destroy 资源泄漏测试。

安全要求：

- 不允许 sudo 启动 QEMU；
- mac helper 安装目录不能被普通用户修改；
- Linux 只 allow piglet0；
- uninstall 不得删除非 Piglet 资源；
- IP/DHCP/gateway 冲突必须提前发现；
- private mode 失败时不要偷偷退回 user mode。

完成后运行 full 四节点验收，并报告每个节点 IP、QMP 状态、SSH、互 ping 与外网结果。

---

# 四、M3 + M4 分段提示词

继续依据 `docs/PRD.md` 完成 Pigsty 迁移与 GA。

必须先读取当前 Pigsty `vagrant/` 目录，再完成：

1. 将以下 spec 转成 Piglet profile：
   - meta
   - dual
   - trio
   - full
   - minio
   - deci
   - simu
   - citus
   - rpm
   - deb
   - all
2. 保留字段和资源语义；
3. 实现 `--image` 与 `--scale`；
4. 写 Pigsty Makefile 集成示例；
5. 提供 legacy `VM_SPEC/VM_IMAGE/VM_SCALE/VM_ARCH` wrapper；
6. 提供从 libvirt-format box 提取 qcow2 的迁移工具/文档；
7. 建立 self-hosted E2E matrix；
8. 覆盖 u22/u24/u26、d12/d13、el8/el9/el10；
9. 完成 GoReleaser；
10. 完成 Homebrew、RPM、DEB；
11. 完成 upgrade/state migration；
12. crash recovery；
13. security review；
14. troubleshooting；
15. 连续 30 次 lifecycle 测试。

不要删除 Pigsty 原有 Vagrant fallback，直到所有 v1.0 验收标准真实通过。给出默认切换和回退方式。

最后输出一份 release readiness report，逐项核对 PRD v1.0 验收标准；任何未真实通过的项目必须标为未完成，不能用“理论支持”替代。
