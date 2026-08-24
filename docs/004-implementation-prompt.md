# Piglet 主执行提示词：供 Claude Code / Codex 实现完整项目

将本文件正文作为实现代理的主提示词使用。它设置的是整个 Piglet v1.0 目标，不是一次性“生成脚手架”的请求。

---

## 主提示词

你正在 `pgsty/piglet` 仓库工作。你的持续目标是：**依据 `docs/003-prd.md` 实现、验证、打包并交付 Piglet v1.0，使 Pigsty 能在 macOS 与 Linux 上直接使用 QEMU 启动单节点 quick VM 和固定私网 IP 的多节点实验室，并由 Piglet 完整拥有默认 VM 使用面，不保留第二 runtime、兼容层或回退路径。**

如果运行环境支持持久 goal/task，请把上面整句设为唯一顶层目标；不要在只完成脚手架、M0、M1 或某个平台后把整个目标标为完成。milestone 只是 checkpoint。

### 1. 需求权威与阅读顺序

开始任何实现前，必须完整阅读：

1. `docs/003-prd.md`：唯一产品与架构执行基线；
2. 本文件：执行纪律与阶段提示；
3. `docs/000-raw-demand.md`、`docs/001-prd.md`、`docs/002-init-promot.md`：不用管，冲突时一律以 003 为准；
4. 当前 Piglet `profiles/`、schema 3 catalog、`packaging/pigsty/vm`，以及
   相邻 Pigsty 仓库（`~/pgsty`）中 profile 对应的 `conf/` 与当前集成入口。

前序 runtime 的一次性迁移输入只存在于 dated evidence，不得重新进入
catalog schema、CI、release workflow 或 runtime dependency。

不要把 PRD 当成免于验证的 QEMU 手册。M0 发现上游行为与 PRD 不符时：

1. 保留失败证据；
2. 用 QEMU/Lima/socket_vmnet/cloud-init/发行版的一手资料复核；
3. 写 ADR，明确事实、选择、fallback 和影响；
4. 若会改变用户承诺、非目标、安全边界或数据语义，一次性请产品 owner 裁决；
5. 未获裁决前不能静默降低标准。

### 2. 不可擅自改变的架构结论

1. 使用独立 Go 程序直接管理 QEMU；不在正常路径调用 `limactl`。
2. Lima 只作 reference/test oracle；不导入完整 `github.com/lima-vm/lima/v2`，不 fork Lima。
3. 不复制 BUSL-1.1 或旧 MPL-2.0 的外部 VM orchestration 源码；只参考公开行为和文档，所有来源与许可证审计必须公开、可复核。
4. 只有一个 hypervisor backend，不建立 provider/plugin framework。
5. v1 仅支持 Linux guest、native arch、macOS/Linux host；非原生 arch 明确拒绝，不静默退回 TCG。
6. QEMU 永远以普通用户运行；特权只收敛在 macOS socket_vmnet 与 Linux bridge/helper 安装边界。
7. 用户配置/profile 是 strict YAML；resolved/state/lease/manifest 是 versioned JSON；运行盘是 qcow2；cloud-init 是 NoCloud CIDATA。
8. 不提供 arbitrary QEMU argv escape hatch。

### 3. 必须守住的终局语义

#### Quick

无 `piglet.yaml` 时，`piglet up` 必须直接工作：

- node `meta`，u24，2 CPU，4GiB memory；
- 64GiB root overlay；
- 默认 64GB sparse `/data`，可用 `--no-data-disk` 关闭；
- login user 默认 `dba`；
- QEMU user NAT，无 sudo/helper；
- SSH 自动 loopback forward；
- 仅 quick 默认增加 loopback：
  - 15432→5432
  - 13000→3000
  - 18080→80
  - 18443→443
- 冲突按 PRD 的确定性有限候选分配并物化到 resolved spec；
- `--no-default-forwards` 可关闭，`--forward` 可追加；
- `piglet init quick` 导出实际 resolved YAML；
- 已存在 quick state 时的新 flags 进入 plan/drift，不得忽略。

#### Private lab

- 一个宿主机全局 RFC1918 IPv4 `/24` 网络，默认 `10.10.10.0/24`；host `.1`；DHCP fallback 只到 `.8`；`.9-.254` 可作 static；默认冲突时允许一次显式、带 warning 的全局 `/24` override；
- `.9` 必须兼容现有 EL9 build node/VIP，`.2-.4` 是 Pigsty guest 内 VIP；
- v1 同时只允许一个 active private project；使用持久 lease 和 exit code 6；
- 每 VM 双 NIC：management user NAT 提供默认路由/互联网/SSH fallback；private NIC 静态、无默认路由、无 DNS；
- host→VM、VM→VM、VM→internet 都必须真实验证；private 失败不能退回 user mode；
- control 最多一个；只有多节点 control 收到项目 private key。

#### macOS private

- pinned upstream socket_vmnet release artifact，先用 Piglet 内嵌 per-arch SHA-256 验证，再安装到 root-only path；同 release 的 SHA256SUMS 不是信任根；
- 可选验证上游 attestation；记录 quarantine/notarization 事实；
- daemon root，QEMU user；禁止 QEMU built-in vmnet 导致整进程 root；
- v1 产品默认使用 host mode；shared 在无冲突替代网段的固定 IP host/peer 原生 E2E 已通过，可作等价 fallback；所有 mode 都必须验证 host `.1` 与 `/24` route，1009 sharing-service 冲突必须 fail closed；是否能禁 DHCP 只是优化，正确性依赖 `.8` 边界而非 disable flag；
- QEMU Unix `stream` + probed reconnect option 优先；Go dial + `ExtraFiles` + `socket,fd=3` 回退；`socket_vmnet_client` 仅人工诊断，不在 runtime chain；
- `network.json` 持久记录 mode/subnet/UUID；禁止 isolation；daemon restart 后验证 reconnect；
- host-mode flag 不足时 fail closed，再提 upstream PR 并等待 release；默认不维护私有 fork。

#### Linux private

- v1 只承诺 systemd + iproute2 + 可用 qemu-bridge-helper host；
- root-owned persistent `piglet0`，不以 root 跑 QEMU；
- Debian/Ubuntu helper 无权限时，installer 记录原状态、拒绝覆盖第三方 override，再用 `dpkg-statoverride --update --add root kvm 4750`；uninstall 必须 `--remove` 并恢复原 owner/mode；禁止用包升级会丢失的临时 setcap；
- RPM 系只验证/报告发行版默认权限，不擅自改；
- `/etc/qemu/bridge.conf` 只维护带独立 marker 的 `allow piglet0` 小块；
- NetworkManager 存在时只把 `piglet0` 标记 unmanaged；
- 干净 Ubuntu 24.04 上完成 install→两 VM→uninstall 无残留 E2E。

#### Image、guest 与 migration

- embedded manifest baseline + 用户显式 `piglet image sync`；sync 使用 minisign-compatible Ed25519 detached signature、至少两把 embedded public key、单调 version 防回滚、离线路径；绝不静默更新；
- 生产签名 private key 不进入仓库、测试 fixture 或日志；测试只用专用 test key；
- remote image 必须 checksum；本地 image 必须先 import 到 digest cache；拒绝 backing/data-file/encryption/unknown incompatible features；
- base 无 backing、只读；overlay 创建显式 `-F qcow2`；运行中禁止 qemu-img；
- login user 是 resolved `ssh.user`，Quick 与全部官方 profile 均为 `dba`；官方 image/转换流水线必须清除已知公开开发 keypair、password、旧 authorized_keys；
- data disk serial 是 96-bit、20-char lowercase base32；guest 用 by-id，fstab 用 filesystem UUID + nofail；
- Piglet-owned profile/inventory contract 固定 schema 3、精确 YAML digest、resolved semantic hash、13 profiles/85 nodes、UID/GID88 `dba`、typed inventory binding/address semantics，以及普通节点 128GiB `/data`、所有 `minio*` 四块 32GiB `/data1..4`；
- `deci/simu` 保持当前不可 scale；不增加 example-only profiles；
- suspend/resume 明确不迁移；不要实现全局危险 `nuke`。

### 4. 开始工作前的仓库与环境审计

先输出并记录：

- cwd、文件清单、git 状态；若不是 git 仓库只报告事实，不覆盖现有文档；
- host OS/arch/version；
- Go version；
- QEMU/qemu-img path/version；
- HVF/KVM 可用性；
- firmware 路径；
- socket_vmnet/helper/bridge 状态；
- 当前可执行的真实 E2E 与缺失 runner；
- 磁盘空间；
- 相邻 Pigsty repo 路径与 commit；
- 三个 owner resource gate：image hosting/manifest signing custody、macOS self-hosted runner、production release OIDC/KMS/publisher custody。

当前任务把本机定义为开发/测试目标。安全、非破坏性的标准依赖安装与验证可以继续；但不得触碰 production host。任何特权网络安装前仍要显示精确 plan、路径、owner/mode、回滚动作。任何删除都必须验证 Piglet ownership 和范围。

若当前 macOS 没有 QEMU，`brew install qemu` 是 M0 的正常开发依赖；能否直接安装取决于执行环境授权。没有授权时给出精确命令并继续不依赖真机的工作，不得声称 HVF E2E 已通过。

创建并持续维护：

```text
TASKS.md
docs/ARCHITECTURE.md
docs/TESTING.md
docs/SECURITY.md
docs/IMAGE_CONTRACT.md
docs/NETWORKING.md
docs/TROUBLESHOOTING.md
docs/decisions/
tests/e2e/
```

TASKS 必须把每个 requirement/milestone 映射到实现、测试、状态与证据链接。

### 5. 实施策略：纵向切片优先

不要先生成几十个空 package。顺序必须是：

1. 最小纵向 quick spike；
2. 用真实 VM 暴露 QEMU/cloud-init/process 问题；
3. 固化 ADR 与 golden；
4. 再提取稳定边界；
5. 进入完整 M1；
6. private 网络另做 M0/M2 gate；
7. 最后迁移 profile、发布矩阵。

外部命令统一用 `exec.CommandContext`/argv slice，有 timeout；显示 command 与执行 argv 分离。不要用 shell 拼接。不要因测试难写而把外部行为藏在全局函数里。

依赖保持小而稳定。可以评估 Cobra、strict YAML、go-diskfs、`x/sys`；每个新依赖说明用途、许可证和为何不使用标准库。不要为了一个小函数导入 Lima 全模块。QMP 只需最小 client，不做完整 QAPI codegen。

---

## 6. M0：必须先通过的技术验证

M0 不承诺最终 API，但必须留下可重放代码、脚本、ADR 和真实日志。

### M0-A：Quick vertical slice

实现并验证：

- platform/capability probe；
- qcow2 base→overlay→offline resize→backing-chain verify；
- 纯 Go CIDATA；
- official u24 native image；
- user NAT + SSH；
- detach strategy；
- QMP greeting/query/status/powerdown/quit；
- stop/start；
- root grow、默认 `/data`、ready generation；
- 当前 host 连续 10 次 lifecycle。

### M0-B：macOS private spike

固定一版 upstream socket_vmnet（PRD 审查基线是 v1.2.2；升级必须有 ADR），验证：

- release artifact/hash/attestation/quarantine/notarization；
- root-only install 与 launchd；
- host/shared mode 与自定义 subnet，并保留旧 shared 受污染失败和干净替代网段 pass 证据；
- host mode 下 host address 是否为 `10.10.10.1`；
- DHCP end `.8`；disable DHCP 能力只记录，不承重；
- no isolation；
- UUID 在 daemon restart 后复用；
- QEMU 8.2.1+ `stream` 与实际 reconnect spelling；
- daemon kill/restart 后 VM 网络恢复；
- Go FD fallback；
- host→VM、VM→VM；
- QEMU user identity。

### M0-C：Linux private spike

在零手工预配置 Ubuntu 24.04 上验证：

- KVM/QEMU；
- helper path/mode；
- reversible dpkg-statoverride；
- bridge.conf marker；
- persistent piglet0；
- NetworkManager coexistence；
- 两 VM host/互通/出网；
- uninstall 恢复原状态且无 tap/bridge/config/override 残留。

另保存 QEMU 6.2 capability/help fixture，确保 Linux floor 的 argv 没有使用高版本专属选项。

### M0-D：guest matrix spike

在 u24、el9、d13 的 native images 上验证：

- login user `dba` 创建与 SSH；
- management/private NIC 按 MAC；
- private 无 default route/DNS；
- root grow；
- by-id data disk、filesystem UUID/fstab；
- cloud-init generation/ready；
- time sync；
- control lateral SSH。

### M0 退出门

只有 `docs/003-prd.md` §4.3 与 §16 M0 的真实退出标准满足，才能大规模进入 M1/M2。若失败，先用同镜像 Lima 对照定位，再写 ADR；不能以 mock/golden 代替。

---

## 7. M1～M4 执行目标

### M1：Quick MVP

完成 003 的 M1 全部 requirement，包括：

- strict config/schema/resolver/plan；
- quick 无 YAML 与导出；
- signed manifest sync/cache/import/prune；
- project/state/atomic writes/lock order/transaction/recovery；
- image/root/data/seed；
- QEMU/QMP/process identity；
- SSH/known_hosts/exec；
- user network/default forwards；
- doctor/status/list/logs/debug/repair；
- `PIGLET_DATA_HOME`；
- 两个 Tier 1 host 的 quick E2E；
- README 与 tarball/Homebrew dev release。

M1 不能只在当前 Mac 通过后宣称跨平台完成。

### M2：Private multi-node

完成：

- macOS/Linux installer/status/uninstall；
- global network state/lease；
- dual NIC；
- parallel nodes、partial failure、safe rollback；
- full/minio profile；
- host→VM、VM→VM、VM→internet；
- daemon/QEMU/CLI crash recovery；
- host reboot；
- 30-cycle no-leak soak；
- 两个 Tier 1 host 真实 private E2E。

### M3：Pigsty migration

完成全部有效 profile topology parity、统一 `dba` identity、Piglet-only
wrapper/Makefile、`--image`、受约束 `--scale`、SSH/hosts install、image
build/migration pipeline 与 Pigsty bootstrap。不得增加第二 runtime/provider
fallback。

### M4：GA

完成 guest matrix（GA 前 7 个 guest × 两个 Tier 1 native arch 共 14 个 entry 各至少一次真实 smoke）、Tier 2 smoke、N-1 state migration、GoReleaser、Homebrew/RPM/DEB/tarball、checksums/signing/attestation、SBOM、security/license review、soak、troubleshooting 与 release readiness report。

---

## 8. 测试与证据纪律

必须区分：

- unit；
- golden；
- integration；
- native real E2E；
- not run；
- failed；
- blocked。

禁止：

- 把 mock/fake QEMU/Lima 对照当 HVF/KVM E2E；
- 把 command generation 当 VM boot；
- 没运行就写 passed；
- 另一平台没有 runner时写“理论支持”；
- 以降级 warning 掩盖 P0 error；
- 用 retry 无限隐藏 race/flakiness。

Native runner 执行：

```bash
go test ./...
go test -race ./...
go vet ./...
staticcheck ./...
```

交叉目标只做 build/compile：

```bash
GOOS=darwin GOARCH=arm64 go build ./...
GOOS=darwin GOARCH=amd64 go build ./...
GOOS=linux  GOARCH=amd64 go build ./...
GOOS=linux  GOARCH=arm64 go build ./...
```

不要用错误 OS 执行 cross-compiled test binary。平台专属测试必须由对应 runner 运行。

每个 E2E 记录：host、OS、arch、QEMU/helper/firmware/image digest、命令、开始/结束时间、结果、日志路径。测试 secret redaction 时使用 canary，不使用真实 key/token。

---

## 9. 安全与数据保护纪律

1. 所有 destroy/prune/uninstall/purge 先 resolve exact target、canonicalize、校验 ownership/root containment/file type；
2. 不对 unresolved env、glob、home、workspace root、XDG root 做递归删除；
3. persistent disk 普通 destroy 保留；删除需要专门 flag + 二次确认；
4. 不能只凭 PID signal；先 QMP identity，再 executable/start-time/argv hash；
5. QMP/runtime/key/seed 权限按 PRD；
6. debug bundle 必须通过 secret canary test；
7. root helper/config 的每个 parent component 检查 owner/mode/symlink；
8. network uninstall 遇 active lease、bridge member 或 ownership 不明就拒绝；
9. project key 普通 destroy 后保留并提示，只由 `project purge-keys` 删除；
10. 生产 manifest private key、signing token、object-store credential 不得生成到仓库。

当前工作树中的既有文件属于用户。不要覆盖 `docs/000`、`001`、`002`、`003`、`004`，除非 M0 证据明确要求且先记录 ADR/获得必要裁决。

---

## 10. 工作产物与汇报格式

每个 milestone 结束时更新 `TASKS.md`，并报告：

1. 完成的 requirement ID；
2. 新增/修改文件；
3. 实际运行的命令与结果；
4. 真实 E2E host/image/digest；
5. `not run`/failed/blocked；
6. state/data/security compatibility；
7. 已知风险；
8. ADR；
9. 下一 milestone；
10. 一次性需要 owner 提供的资源/裁决。

若三项 owner gate 尚未具备，不要停在空泛提问：

- 使用 test-only image source/key 完成不依赖生产 secret 的实现；
- 使用当前可用真机完成本地 E2E；
- 继续所有不依赖发布日历的工作；
- 到相应 release gate 再把缺失项标为 blocker。

### 目标完成条件

只有 `docs/003-prd.md` §17 的 v1.0 验收全部真实满足、M0～M4 required gates 通过、release readiness 无 P0 blocker 时，才可宣告顶层目标完成。

现在开始：先完整阅读 003、当前 Piglet 实现和 Pigsty 集成面，审计仓库/环境，创建 TASKS 与 M0 evidence plan；随后实现最小 quick vertical slice并运行当前 host 的真实 smoke。不要停在计划或空脚手架；持续推进到 M0 退出门，或出现经过安全替代方案穷尽后仍无法由代码解决的明确外部 blocker。
