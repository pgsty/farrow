# Farrow 重构指令：产品方向重定向

> **后续断代（2026-08-27 晚）：产品负责人裁决"全局有且仅有一套 deployment"，
> 本文 D5（两层 project 结构）与 D8.2（多项目租约路线）被显式推翻并已执行：
> project/lease 概念连根删除，状态目录重整为 ~/.farrow/{state.json,images/,
> keys/,nodes/,disks/,locks/}，配置发现 farrow.yml > pigsty.yml。现状以
> docs/ 与 git log（quick 火化 → project 消失 → lease 切除 → images/ →
> farrow.yml → 配置面清理）为准。**
>
> **执行记录（2026-08-27）：本指令已在本仓库执行完毕，不要重复执行。**
> P0（`699440d`）、P1（`34118e5`）、P2（`121b81b`）、P3（`b71aa10`）、
> P4（`c7375e4`）、P5 文档（`f077da3`）已按阶段提交，全部质量门禁
> （test/race/vet/staticcheck/四目标交叉编译）通过。仍然开放的事项见
> docs/status.md 与 docs/phase-2.md：原生硬件重放（含 EL 上的 NM 后端）、
> D3 第二步冷收敛（`up --restart` 预留位）、以及 D8 的全部暂缓项。
> 执行中的两处忠实偏差：`farrow ss` 的 fragment 本就安装裸节点名别名
> （`ssh meta` 直接可用），故未增加 `--flat` 开关；P3 的 destroy 节点
> 选择器与 P1 的节点级重建共用机制，提前随 P1 落地。

> 本文档是 2026-08-27 产品负责人（冯若航）与 Claude 深度设计评审的结论。
> 执行期间它是最高优先级的权威指令；执行完毕后作为设计依据存档，现状以
> docs/ 为准。

## 你的角色与任务

你接手 pgsty/farrow 仓库（pre-1.0，Go 单二进制 QEMU VM 管理器，Pigsty 的
Vagrant 替代品），执行一次**产品层面的方向重定向**。评审的总体判断是：

- **工程底座是优秀的，必须完整保留**——事务日志、进程身份元组、属主/路径
  边界检查、崩溃恢复、fail-closed 解析、签名镜像链，这些不许倒退。
- **产品叙事有四处方向性错误，必须纠正**——默认模式选错了受众、配置文件
  造了第二份事实、EL/桌面 Linux 被错误地排除、生命周期模型是项目粒度而
  非节点粒度。

按下文的决策与阶段执行。pre-1.0 允许干净断代：**不做旧格式兼容**，做清晰
报错和迁移提示。

## 不可动摇的护栏（全程保持）

1. QEMU 永远以调用用户身份运行；原生架构 + 原生加速，无 TCG 回退。
2. 无任意 QEMU 参数透传、无 provider/plugin 框架。
3. 破坏性操作保持属主、身份、路径三重边界；歧义状态保留并报告，不猜测。
4. 特权组件永不执行用户可写二进制或 shell 字符串（`sudo -n -- argv` 模型不变）。
5. **配置的缺席永远不推导出销毁**（新增铁律，贯穿新生命周期模型）。
6. 双网卡拓扑不变：管理网卡 slirp（DHCP/DNS/默认路由/出网）+ 私有网卡固定
   IP。macOS vmnet **host 模式默认**维持（ADR-0004 成立）；slirp 出网的性能
   代价已评审接受，只需写入文档（含 `proxy_env` / 本地 mirror 规避建议）。
7. 特权伴生二进制维持**恰好一个**（`farrow-hosts-helper`），架构不变：
   RPM/DEB 渠道包内直接 root 属主安装；brew/tarball 渠道由 setup 验 digest
   后拷入 `/opt/farrow/libexec`（brew prefix 是用户属主的，不能作为 sudoers
   目标——这是该设计存在的原因，文档要写明白）。

---

## 决策与规格

### D1 · 默认即固定 IP 网络，且是唯一的模式

- `farrow setup`（无参数）= 生成单节点默认 lab（10.10.10.10，全默认值）的
  `pigsty.yml` + 准备私有网络。首跑过一次 sudo 是已接受的 trade-off。
- **文档中不再出现 "private mode" 这个名字**。对用户而言只有一种模式，
  不命名、不解释、不对比。
- `network.mode: user` 从声明式配置 schema 中**删除**，随之删除：单节点特例
  校验、`forwards` 配置段、`--no-default-forwards`、`requested_host` 端口重映射
  兼容机制。
- **slirp 代码路径保留**（管理网卡仍用它；将来 `farrow play` 复用它），删除的
  只是 "user 网络作为一种项目类型"。
- Quick 的正当场景由未来的 **`farrow play`** 承接（minikube 式单例
  playground：每用户一台、固定名字、状态在 `~/.farrow/play/`、与 cwd 无关、
  无 marker 无配置文件、全 flags、slirp+默认转发）。**本次不实现**，属于
  暂缓项（见 D8），但重构时不得堵死它的实现路径。

### D2 · 配置 = Pigsty inventory（inventory-as-config）

配置文件就是 Ansible inventory 形状的 YAML，与 pigsty.yml 同一格式。

**发现与识别**：`-f` 显式 > `./farrow.yaml` > `./pigsty.yml`（farrow.yaml 仅是
文件名兼容，内容同格式）。顶层存在 `all:` 即 inventory 模式。遇到旧
`version: 1` + `nodes:` 格式 → 硬错误 + 一句迁移指引，不做静默兼容。

**解析边界**：`vm_*` 命名空间与白名单变量内部严格（未知 vm_ 变量、错类型、
含 Jinja `{{ }}` 模板的值 → 报错）；命名空间外的一切 Pigsty 参数完全不读不
校验。v1 范围：单 YAML 文件；不支持 inventory 目录、INI、动态 inventory、
vault、Jinja 求值。

**节点集与推导**：
- `all.children.*.hosts` 按 IP 去重合并为被管主机集；上限 20 节点。
- CIDR 从主机地址推导：全部被管主机必须同一 RFC1918 `/24`，否则报错。
  地址布局维持 `.1` host、`.2–.8` DHCP、`.9–.254` 静态。
- 同一主机从多个 group 继承到不一致的同名 `vm_*` 值 → **硬错误**，提示落到
  host var。不实现 Ansible 的 group depth/字母序仲裁（显式简化，写入文档）。
- 生效链：内置默认 < `all.vars` < group vars < host vars。

**`vm_*` 变量规格**（全部有默认值；裸主机条目 `10.10.10.13: {}` 必须能起来
一台完整默认机器）：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `vm_skip` | `false` | `true` = 此主机不由 farrow 管理（真机/外部节点） |
| `vm_image` | `u24` | 镜像别名 |
| `vm_cpu` | `2` | 核数 |
| `vm_mem` | `4096` | 整数 = MiB；也接受 `"8GiB"` 带单位字符串 |
| `vm_disk` | `64` | 根盘，整数 = GiB |
| `vm_disks` | `[{path: /data}]` | 额外数据盘数组，条目结构见下 |
| `vm_alias` | `[]` | 发布到 /etc/hosts 的别名 |
| `vm_shares` | `[]` | 9p host 目录共享（沿用现有语义） |

`vm_disks` 条目：

| 字段 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `path` | 是 | — | 挂载点，节点内唯一 |
| `size` | 否 | `128` | 整数 = GiB |
| `fs` | 否 | `xfs` | `xfs` 或 `ext4`（原 `auto` 取消） |
| `persistent` | 否 | `false` | destroy 后保留、下次 up 重挂 |

- **无 `name` 字段**：磁盘序列号与 persistent 重挂身份统一从 `path` 派生。
- 不想要盘：显式 `vm_disks: []`（空列表也是覆盖）。

**没有 `vm_name`**。VM 名规则（依次回落，派生结果必须项目内唯一，冲突报错）：
1. host var `nodename`（Pigsty 原生变量，同时决定 hostname，两边天然一致）；
2. 主机唯一隶属于某 PG 集群时 → `<pg_cluster>-<pg_seq>`（镜像 Pigsty
   `node_id_from_pg` 惯例；多集群归属歧义时要求显式 `nodename`）；
3. `node-<IP末段>`。

**复用的 Pigsty 原生变量白名单**（读取但不加 vm_ 前缀，写死并文档化）：
host key（IP → 私有网卡地址）、`nodename`、`pg_cluster`/`pg_seq`（仅用于
名字派生）、`admin_ip`（→ control 节点：lateral key、别名发布）、
`node_admin_username`（默认 `dba`）、`node_admin_uid`（默认 `88`）→ guest 账号。

**Drift 域**：resolved spec 与 hash 只对**提取后的 VM 子集**（逐节点）计算。
编辑 `pg_*`、`node_*` 等任何命名空间外变量不产生 drift。

### D3 · 节点粒度生命周期模型（增量默认）

`farrow plan` = 逐节点 diff(desired, applied)，分类如下：

| 类别 | 触发 | 行为 |
|---|---|---|
| 新增 | 配置有、状态无 | `up` 直接创建，**不碰已有节点** |
| 冷收敛 | 盘扩容、加数据盘、`vm_alias` | 停机窗口应用，不重建根盘（第二步实现） |
| 重启级 | `vm_cpu`、`vm_mem`、`vm_shares` | exit 4 报告；`up --restart <node>` |
| 重建级 | `vm_image`、IP、名字、缩盘、改 fs | exit 4 报告；`recreate --force <node>` |
| 消失 | 配置删除该主机 / `vm_skip: true` | **仅报告，永不自动删**；显式 `destroy <node> --force` |

- `destroy` 因此需要接受节点选择器（原"整项目销毁"保留为无参形态）。
- 配置文件丢失：`start/stop/status/destroy` 照常（吃 resolved 状态），`up`
  报错指引，绝不理解为删除意图。
- 换配置文件 = 对同一项目（目录 marker 锚定身份）提出新期望状态，走同一
  diff。想要全新项目 = 换目录。
- **实施分两步**：第一步（与 D2 同批交付）：新增节点直接创建 + 重建降到
  节点粒度 + 消失节点报告，其余变更字段暂全归重启/重建级。第二步：字段级
  冷收敛（seed 重生成、generation 契约配合）。第一步不交付，D2 的
  scale-out 工作流就是残废——两者必须绑定。

### D4 · Linux NetworkManager 后端（高优先级，产品负责人明确要求）

现状"RHEL 家族与 NM 管理的主机不支持私网"是 v1 范围决策而非技术必然，
且把难度搞反了（激活休眠 networkd 的 wifi 误报死角正是现在 status.md 的
头号 rough edge）。改为**按当家人选后端**：

- `NetworkManager.service` 活跃 → **NM 后端**：nmcli（或 D-Bus）创建
  `farrow0` 网桥（`ipv4.method manual` `.1/24`、`ipv6.method disabled`、
  autoconnect），**永不激活 networkd**。firewalld 活跃时处理 zone
  （`connection.zone trusted` 或专用 zone，取其一并文档化）。私有桥不需要
  DHCP（静态 IP 走 cloud-init），无 dnsmasq。
- networkd 活跃 → 现有后端不变。
- 两者皆无 → 明确不支持并直说。
- bridge.conf 标记块、helper 权限核验、manifest/journal 记录前态与精确回滚
  ——与现有 networkd 后端同等级的事务模型，uninstall 完整恢复。
- 验收目标：EL9 系（RHEL/Rocky/Alma 9）+ NM 管理的 Ubuntu 桌面。EL8 宿主机
  不在范围。现有 `90-farrow-unmanaged.conf` 逻辑并入新后端的职责划分。

### D5 · 项目状态与孤儿治理

现有两层布局（cwd `.farrow/project.json` marker + `~/.farrow/projects/<uuid>/`
重状态）是正确的，**不动骨架**，补四个增量：

1. marker（cwd 与数据根两份）schema 升级，增加 `work_dir` 与 `name` 字段；
   经 `project upgrade-state` 提供迁移。
2. `farrow list` / `farrow project list` 显示 `work_dir` 并标注孤儿（目录不存在，
   或存在但 marker 的 uuid 不匹配）。
3. 新命令：
   - `farrow project rm <uuid> --force`：数据根侧直接销毁（先按身份元组停
     VM、释放私有租约，再删工件/keys/项目壳）；
   - `farrow project prune`：批量版，**默认 dry-run、永不自动执行**，输出必须
     含 work_dir 路径（防"未挂载的移动硬盘卷"假阳性）；
   - `farrow destroy --force --purge`：工件 + persistent 盘 + keys +
     `projects/<uuid>` + 本地 marker 一次清完。
4. 首次 `up` 在 `.farrow/` 写入内容为 `*` 的 `.gitignore`。
5. 错误信息改进：数据根侧项目缺失/marker 不匹配时，直接打印
   "`rm -rf .farrow` 后重新 `up`" 的指引。

### D6 · CLI 动词与 Vagrant 对齐

| 项 | 规格 |
|---|---|
| `halt` | 新增，`stop` 的别名 |
| `reload` | 新增，语义 = `up --restart`（重读配置并应用重启级 drift），**不是** `restart` 的别名 |
| `init` | 改为默认写 `./pigsty.yml`（已存在则拒绝，`--force` 覆盖）；`-o -` 保留 stdout。内置模板缩减为**纯拓扑通用模板** `meta`(1)/`dual`(2)/`trio`(3)/`full`(4)：只有节点、IP、`vm_*`，无任何 Pigsty 服务配置 |
| `ss --flat` | 新增：SSH 别名不加目录前缀（直接 `ssh meta`），冲突自负 |
| `hosts install` | 解除"仅默认 10.10.10.0/24"限制，接受项目自身子网；`up` 成功后检测到别名未发布时打印一句建议命令 |
| `provision` | 现状（`--script` ad-hoc）保留，声明式 `vm_provision` 属暂缓项（D8） |
| suspend/resume/snapshot | 维持 out of scope / phase-2 提案不变 |

### D7 · 删除清单

| 删除对象 | 说明 |
|---|---|
| 13 个内嵌 Pigsty profile（minio/citus/all/oss/pro/deb/rpm/deci/simu 及其目录绑定） | 拓扑知识迁往 Pigsty 仓库 conf 模板（`vm_*` 变量），属 Pigsty 仓库的配套任务，farrow 侧只管删 |
| `farrow pigsty inventory` 子命令 + `internal/pigsty` 渲染/改址/digest sidecar 机制 | 整个双文件对账子系统作废（ADR-0010 标记 superseded） |
| `pigsty-vm` 包装器 | 一体化后无存在意义 |
| `network.mode: user` 及连带 schema（见 D1） | slirp 代码路径本身保留 |
| `cmd/farrow-*-m0`、`cmd/farrow-net-stage`、`cmd/farrow-linux-net-stage` | M0 取证残留，移入 `tools/` 或删除；发布物本就只有 `farrow` + `farrow-hosts-helper` |

### D8 · 明确暂缓（不做，但不许堵死）

1. **`farrow play`**：设计已定（见 D1），成熟前不发布、不写文档。
2. **多私有项目并存**：默认改私网后单租约限制会更快暴露（第二个项目
   exit 6）。方向已定——一座桥一个 `/24`，项目从中**租借地址**而非独占网络，
   主机级注册表仲裁冲突——但需要独立设计评审后再实施。本次重构中在文档
   里如实写明"当前同时一个 lab"的限制即可。
3. **`vm_provision`** 声明式钩子、字段级冷收敛全量、macOS 单网卡 shared
   拓扑 / Linux 桥 masquerade（高带宽出网 opt-in）。

---

## 实施阶段

| 阶段 | 内容 | 依赖 |
|---|---|---|
| P0 清障 | D7 的 m0/stage 清理；文档纠错（站点 `FARROW_DATA_HOME` → 代码实际的 `FARROW_HOME`）；doctor 判定拆分（能力缺失 vs 私网未装分开，私网未装不再整体 exit 3）；marker schema v2（work_dir/name）+ 迁移 | 无 |
| P1 配置革命 | D2 inventory 解析 + `vm_*` 全规格 + D3 第一步（增量创建/节点级重建/消失报告）+ D1 默认私网与 user 模式删除 + D7 其余删除 + `init` 新行为 | P0 |
| P2 平台 | D4 NM 后端 | 可与 P1 并行 |
| P3 生命周期 | D5 孤儿治理三命令 + `--purge` + destroy 节点选择器 | P1 |
| P4 UX | D6 其余（halt/reload/ss --flat/hosts 子网） | P1 |
| P5 收尾 | D3 第二步冷收敛；文档全量重写定稿 | P1–P4 |

P1 是一次成体系的 breaking change，**作为一个整体交付**，不拆成兼容碎片。

## 验收标准（黄金路径）

1. 空目录：`farrow setup && farrow up` → 一台 10.10.10.10，
   `ssh dba@10.10.10.10` 通、host `ping` 通。
2. 含裸主机条目 `10.10.10.13: {}` 的 4 节点 pigsty.yml → `up` 起 4 台，裸条目
   得到全默认（u24/2c/4096MiB/64G 根盘/128G xfs `/data`），VM 名按
   nodename → pg 派生 → node-13 规则落定。
3. 向运行中 lab 的配置追加一台主机 → `plan` 只显示一条"新增"；`up` 创建
   它，已有节点 uptime 不断。
4. 修改配置中任意 `pg_*`/`node_*` 变量 → `plan` 显示无 drift。
5. `rm -rf` 项目目录 → `farrow project list` 标注孤儿 →
   `farrow project rm <uuid> --force` 彻底清干净（含运行中 VM 与租约）。
6. Rocky 9（NM 当家，firewalld 开启）：`farrow setup` 经 NM 后端装网、full lab
   全通、`network uninstall` 精确恢复前态。
7. 旧 `version: 1` farrow.yaml → 一条清晰报错 + 迁移指引，无静默行为。
8. 全套现有质量门禁通过：unit / race / vet / staticcheck / govulncheck /
   四目标交叉构建。

## 文档与 ADR 要求

- docs/ 全量按新叙事重写：**单一模式**（不出现 private 命名对比）、`vm_*`
  配置参考、drift 分类表、孤儿救援 runbook、slirp 出网说明（含 proxy_env /
  本地 mirror 建议）、sudoers drop-in 说明
  （`NOPASSWD: /opt/farrow/libexec/farrow-hosts-helper`）。
- 新增 ADR：inventory-as-config、节点粒度 drift、NM 后端、默认私网；
  ADR-0010 标记 superseded；ADR-0001 中 quick 默认相关条目修订。
- status.md 保持"verified / not verified"的诚实文化：重构后的每项能力按仓库
  既有 evidence 纪律重新取证，不继承旧证据的结论。
- farrow.pgsty.com 站点仓库的同步更新是后续独立任务，本仓库文档先行。

## 工作方式

- 动手前先读：`docs/architecture.md`、`docs/networking.md`、`docs/cli.md`、
  `docs/config.md`、`internal/project/project.go`、`internal/config/`、
  `internal/network/{linux,darwin}/`，对齐本文档与现实的差异。
- 按阶段推进，每阶段独立成 commit（组），docs 与代码同批更新。
- 涉及真实宿主机网络变更的原生验证，遵循仓库既有安全惯例：先 dry-run/
  计划输出，需要特权或可能影响宿主机连接的操作先征得操作者确认。
- 本文档已定的决策直接执行，不再重新论证；执行中发现本文档**未覆盖**的
  真设计空白（而非实现细节），列出问题与你的建议方案，交产品负责人裁决，
  不要自行发明方向。
