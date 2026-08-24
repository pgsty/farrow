# Piglet 原始需求规格说明书

- **文档类型**：需求基线 / Product Requirements Specification
- **项目暂定名**：Piglet
- **所属项目**：Pigsty
- **文档版本**：v0.1
- **状态**：待产品负责人确认
- **目标读者**：Pigsty 维护者、Piglet 实现者、Codex/AI 编码代理
- **最后更新**：2026-08-23

---

## 1. 文档目的

本文档用于把围绕 Piglet 已经讨论清楚的原始需求，整理成一份可以直接用于产品设计、技术设计、任务拆分和验收的需求基线。

本文档刻意区分四类内容：

1. **明确需求**：已经直接表达，必须实现；
2. **推导需求**：为了满足明确需求而必然需要具备的能力；
3. **当前技术决策**：讨论后形成的推荐实现，但原则上可以被等价方案替代；
4. **待确认事项**：尚未真正拍板，不能由实现者自行假定。

本文档不是让实现者重写一个通用 Vagrant、Lima 或 libvirt。实现者不得把未确认的技术建议擅自升级为产品硬性需求。

---

## 2. 背景

Pigsty 当前使用 Vagrant 管理本地虚拟机环境：

- macOS 主要使用 VirtualBox provider；
- Linux 主要使用 vagrant-libvirt；
- 两套路径承担相同的本地开发、测试和演示任务；
- 两套模板存在大量重复逻辑；
- 两边的镜像格式、网络配置、磁盘配置、安装方式和调试路径并不一致；
- 使用者需要理解 Vagrant、VirtualBox、libvirt、QEMU、provider、box 等多层概念，体验割裂。

当前 Pigsty 的虚拟机规格本身并不复杂，核心字段已经收敛为：

```text
name
ip
cpu
mem
image
root_disk
data disk(s)
```

现有 `full` 环境本质上也是一组同构节点：一个 `meta` 节点与三个普通节点，分别使用固定地址 `10.10.10.10-13`。 

外部条件也在变化：HCP Vagrant 公共 box registry 已公布退役时间表，Vagrant CLI 会保留，但公共 box 托管链路将在 2027 年 6 月 7 日彻底下线。Pigsty 无论是否继续使用 Vagrant，都需要接管镜像构建、版本固定与分发。

原始讨论首先明确了三个基础诉求：

1. 一键拉起多台虚拟机；
2. 支持多种主流 Linux 操作系统；
3. 能够简单配置内部网络。

随后进一步具体化为：

- 8 个主要操作系统版本；
- ARM64、AMD64 两种架构；
- VM 之间通过 IP 直接互通；
- 宿主机能够访问主要节点；
- 使用类似 Vagrantfile 或 Terraform 的配置文件统一定义。

---

## 3. 产品愿景

### 3.1 一句话定义

> Piglet 是一个用 Go 编写、以 QEMU 为唯一虚拟机后端、同时运行在 macOS 与 Linux 上的 Pigsty 本地虚拟机管理组件，用来替代当前 Vagrant + VirtualBox/libvirt 的分裂体验。

### 3.2 目标状态

用户面对的是同一套命令、同一份配置和同一种资源模型：

```text
macOS  -> Piglet -> QEMU -> HVF
Linux  -> Piglet -> QEMU -> KVM
```

用户不应再需要理解或选择：

```text
Vagrant provider
VirtualBox
vagrant-libvirt
libvirt daemon
Vagrant box registry
Ruby plugin
```

### 3.3 核心价值

Piglet 的核心收益不是宣称 QEMU 必然比 VirtualBox 快多少，而是：

- 统一 macOS 与 Linux 的虚拟机管理体验；
- 统一镜像格式与镜像供应链；
- 统一配置模型与生命周期命令；
- 保留原生硬件虚拟化性能；
- 降低 Pigsty 本地实验环境的安装和维护成本；
- 为单节点体验、四节点 HA、构建矩阵和 20 节点模拟提供同一个底座。

---

## 4. 产品范围

### 4.1 Piglet 必须解决的问题

Piglet 必须能够在一台 macOS 或 Linux 宿主机上：

1. 拉起一台或多台 Linux 虚拟机；
2. 为每台 VM 指定名称、镜像、CPU、内存、IP 与磁盘；
3. 支持 VM 之间通过固定内部 IP 直接通信；
4. 允许宿主机访问 VM，至少必须能访问主要控制节点；
5. 支持 VM 访问互联网，以安装操作系统包与 Pigsty 依赖；
6. 提供类似 Vagrant 的创建、启动、停止、状态、SSH 与销毁体验；
7. 在 macOS 与 Linux 上使用同一份声明式配置；
8. 在原生架构上使用硬件虚拟化，而不是默认做跨架构软件模拟；
9. 直接服务 Pigsty 的本地开发、测试、演示与发行版兼容性验证。

### 4.2 Piglet 不解决的问题

Piglet 不是：

- 通用云基础设施编排工具；
- 生产级多租户虚拟化平台；
- 完整的 Vagrant 兼容实现；
- 完整的 Lima 或 libvirt 重写；
- 容器运行时；
- 远程数据中心 VM 管理平台；
- 用于运行不可信镜像的安全沙箱。

---

## 5. 目标用户与使用场景

### 5.1 Pigsty 核心开发者

需要在本机快速启动 `meta`、`full`、`minio`、`simu` 等环境，调试：

- Pigsty Ansible 逻辑；
- PostgreSQL 高可用；
- Patroni、etcd、HAProxy、VIP；
- Silo/MinIO 多盘部署；
- 监控、日志与备份；
- Kafka、MySQL、Redis、ClickHouse 等附加模块。

### 5.2 发行版与打包维护者

需要在不同 Linux 发行版与 CPU 架构上验证：

- 安装是否成功；
- 包依赖是否正确；
- systemd、SELinux、NetworkManager 等发行版差异；
- ARM64 与 AMD64 的兼容性。

### 5.3 普通 Pigsty 用户

希望获得类似 minikube 或 Colima 的体验：

```bash
piglet up
piglet ssh
piglet stop
piglet destroy
```

用户不需要理解底层 provider，也不需要手工写 QEMU 命令。

### 5.4 文档、教程与演示使用者

需要启动一个确定版本、确定资源、确定地址的环境，使教程和文档中的地址、节点名与行为可以重复。

---

## 6. 优先级定义

| 级别 | 含义 |
|---|---|
| **P0** | Piglet 能否替代现有 Vagrant 主路径的决定性需求 |
| **P1** | 正式替代 Pigsty 当前主要测试环境所必需 |
| **P2** | 重要增强，但不阻塞首个可用版本 |
| **OPEN** | 尚未拍板，必须由产品负责人确认 |

---

# 7. 功能需求

## 7.1 平台与架构

### HOST-001 [P0] 支持 macOS 与 Linux

Piglet 必须运行在：

- macOS；
- Linux。

Windows host 不在当前范围。

### HOST-002 [P0] 一级支持平台

首要验证平台为：

- Apple Silicon macOS，ARM64；
- Linux AMD64。

这是当前最重要、最常用的两条数据路径。

### HOST-003 [P1] 二级支持平台

后续应支持：

- Intel macOS，AMD64；
- Linux ARM64。

二级平台可以晚于首个 MVP，但数据模型和实现不得从根本上阻止它们。

### ARCH-001 [P0] 只要求原生架构运行

Piglet 不要求：

- ARM 宿主机运行 AMD64 guest；
- AMD64 宿主机运行 ARM64 guest。

Piglet 必须自动识别宿主机架构，并选择同架构镜像。

### ARCH-002 [P0] 不得静默退化到 TCG

当用户请求非原生架构时，默认行为必须是拒绝并给出清晰错误，而不是静默切换到极慢的软件模拟。

是否允许提供显式实验参数开启 TCG，属于 P2/OPEN，不得成为默认行为。

---

## 7.2 Guest 操作系统与测试矩阵

### OS-001 [P0] 支持多种主流 Linux 发行版

Piglet 只要求 Linux guest，不要求 Windows 或 macOS guest。

### OS-002 [P1] 初始 8 个主要系统版本

与当前 Pigsty 主矩阵保持一致，初始至少覆盖：

```text
EL 8
EL 9
EL 10
Debian 12
Debian 13
Ubuntu 22.04
Ubuntu 24.04
Ubuntu 26.04
```

这 8 个系统版本分别应有 AMD64 与 ARM64 镜像，形成 16 个组合。当前 Pigsty 的镜像映射与版本固定逻辑已经围绕这一组别名组织。

### OS-003 [P1] EL 发行版变体

Rocky Linux 作为默认 EL 实现。AlmaLinux、Oracle Linux、RHEL 等变体可以通过显式镜像条目支持，但不要求在首个 MVP 中全部成为 Tier 1。

### OS-004 [P0] 镜像版本必须可固定

相同配置在不同日期运行时，不能因为 `latest` 漂移而得到不可预测的系统版本。

配置或镜像 manifest 必须能固定：

- 发行版；
- 主版本；
- 精确镜像版本；
- 架构；
- 校验摘要。

---

## 7.3 虚拟机资源模型

### VM-001 [P0] 节点名称

每台 VM 必须有唯一名称，并作为：

- CLI 目标；
- hostname；
- SSH alias；
- 状态目录标识；
- 日志标识。

### VM-002 [P0] CPU

每台 VM 可以指定 vCPU 数量。

### VM-003 [P0] 内存

每台 VM 可以指定内存大小，配置必须支持带单位表达，例如：

```text
2048MiB
4GiB
```

### VM-004 [P0] 镜像

每台 VM 可以：

- 使用 profile 默认镜像；
- 单独覆盖镜像；
- 按宿主机架构自动选择对应变体。

### VM-005 [P0] 根盘

每台 VM 可以指定根盘虚拟容量。

根盘应基于只读基础镜像创建写时复制实例盘，而不是为每台 VM 完整复制基础镜像。

### VM-006 [P0] 数据盘

每台 VM 可以指定零块、一块或多块独立数据盘。

数据盘至少要支持：

- 名称；
- 容量；
- 挂载点；
- 文件系统策略；
- 是否在 recreate 或 destroy 时保留。

### VM-007 [P1] MinIO/Silo 四盘场景

Piglet 必须能够表达并稳定创建：

- 单节点四数据盘；
- 多节点 × 四数据盘环境。

现有 Vagrant 模板会为 MinIO 节点创建四块 32GB 数据盘，Piglet 必须覆盖相同能力。 

### VM-008 [P0] 配置的确定性

给定相同版本的 Piglet、相同配置和相同基础镜像，生成的以下信息应保持确定性：

- MAC；
- 节点名；
- IP；
- 磁盘名称；
- SSH alias；
- 状态目录。

---

## 7.4 单节点体验

### SINGLE-001 [P0] 单节点必须是一等场景

Piglet 必须提供一个类似 minikube 或 Colima 的单节点 Pigsty 沙箱，而不是把单节点当作多节点的偶然特例。

典型使用方式：

```bash
piglet init meta
piglet up
piglet ssh meta
```

### SINGLE-002 [P0] 单节点低摩擦

单节点模式应尽可能做到：

- 不要求用户理解 bridge、TAP、vmnet；
- 不要求每次启动输入 sudo；
- 自动配置 SSH；
- 自动输出 PostgreSQL、Grafana 等服务访问方式。

### SINGLE-003 [OPEN] 单节点默认网络语义

讨论中形成了两个可行方向，但尚未由产品负责人最终拍板：

1. 默认要求宿主机直接访问 guest 固定 IP；
2. 默认使用无特权 NAT + 自动端口转发，直接 IP 作为增强模式。

本文档把“私网固定 IP”列为完整多节点模式的硬要求。单节点默认模式仍需确认。

---

## 7.5 多节点编排

### MULTI-001 [P0] 一份配置定义多台 VM

用户必须能够在一个文件中定义多台 VM，并用一条命令统一拉起：

```bash
piglet up
```

### MULTI-002 [P0] 允许按节点操作

用户应能对单个或部分节点执行：

```bash
piglet up node-1
piglet stop node-2 node-3
piglet ssh meta
piglet destroy node-1
```

### MULTI-003 [P1] 支持现有规模

Piglet 最终需要覆盖 Pigsty 当前常见规模：

- 1 节点 `meta`；
- 2 节点 `dual`；
- 3 节点 `trio`；
- 4 节点 `full`；
- 4 节点多盘 `minio`；
- 10 节点 `deci`；
- 20 节点 `simu`；
- Citus、RPM、DEB、ALL 等构建与测试规格。

### MULTI-004 [P1] 并行启动

多节点启动应支持有上限的并发：

- 避免 20 台 VM 完全串行；
- 避免同时启动过多 VM 造成宿主机失控。

### MULTI-005 [P0] 部分失败必须可诊断

若部分节点启动成功、部分失败：

- 成功节点不得被默认销毁；
- 命令必须明确列出成功、失败和未执行节点；
- 必须保留失败节点的日志和最终 QEMU 参数；
- 必须返回非零退出码。

---

## 7.6 网络

### NET-001 [P0] 固定内部 IP

在完整多节点模式中，每台 VM 必须能够指定一个稳定内部 IP，例如：

```text
meta    10.10.10.10
node-1  10.10.10.11
node-2  10.10.10.12
node-3  10.10.10.13
```

### NET-002 [P0] VM 之间按 IP 直接互通

同一实验环境中的 VM 必须能够通过内部 IP 双向通信，不得要求通过宿主机端口转发中转。

### NET-003 [P0] 内部流量走内部网络

访问实验环境内部网段时，连接必须走私网网卡。

私网网卡不得：

- 抢占默认路由；
- 覆盖 guest 的默认 DNS。

现有 VirtualBox 模板已经显式删除私网网卡上的默认路由，说明这是 Pigsty 当前明确的网络语义。

### NET-004 [P0] Guest 可访问互联网

VM 必须可以访问外部软件仓库和互联网，用于：

- 安装依赖；
- 下载 Pigsty；
- 安装 PostgreSQL 与扩展；
- 获取时间与软件元数据。

### NET-005 [P0] 宿主机访问主要节点

宿主机必须能访问主要控制节点。访问方式至少满足一种：

- 直接通过内部 IP；
- Piglet 自动配置的稳定端口映射与 SSH alias。

### NET-006 [P1] 宿主机直接访问全部私网节点

在完整 private 模式中，宿主机应能直接访问所有 VM 的固定私网 IP，而不仅是 `meta`。

### NET-007 [P0] 不得以 root 运行完整 QEMU

即使需要特权网络辅助组件，也不得让整台 QEMU 进程长期以 root 身份运行。

### NET-008 [P0] 网络安装必须可自动化和诊断

如果 private 网络需要一次性安装辅助组件：

- 必须由 Piglet 提供明确安装命令；
- 安装过程应幂等；
- `doctor` 能检查状态；
- 日常 `up/start/stop` 不应反复请求 sudo；
- 卸载不得破坏非 Piglet 创建的网络。

### NET-009 [P1] 双网卡模型

推荐并基本符合现有需求的网络模型是：

```text
NIC 1: 管理/NAT，负责默认路由、互联网、SSH fallback
NIC 2: 固定私网，负责节点内部流量与宿主机直连
```

这是当前技术方向，但如果能够使用更简单的方案完整满足 NET-001 至 NET-008，可以提出替代设计。

---

## 7.7 镜像管理

### IMAGE-001 [P0] 不依赖 Vagrant Registry

Piglet 不能把 HCP Vagrant Registry 作为运行时依赖。

### IMAGE-002 [P0] 使用 Pigsty 自主控制的镜像源

镜像可以来自：

- Pigsty 自有对象存储或静态 HTTP；
- 本地绝对路径；
- 将来可扩展的镜像 manifest。

### IMAGE-003 [P0] 镜像校验

远程镜像必须具有 SHA-256 或等价强校验。

下载未完成或校验失败的文件不得进入有效缓存。

### IMAGE-004 [P0] 基础镜像缓存

相同镜像在同一宿主机上只下载一次。

多台 VM 共享只读基础镜像，各自使用独立 overlay。

### IMAGE-005 [P0] 架构匹配

同一个镜像逻辑名称可以包含：

```text
amd64 variant
arm64 variant
```

Piglet 根据宿主机架构选择正确变体。

### IMAGE-006 [P1] 迁移现有 libvirt box

当前 libvirt Vagrant box 中的 qcow2 `box.img` 应能被提取、归一化并纳入 Piglet 镜像供应链。

### IMAGE-007 [P1] 镜像契约

正式支持镜像至少需要：

- cloud-init 或等价自动初始化能力；
- OpenSSH server；
- virtio 磁盘与网卡驱动；
- serial console；
- 根文件系统可扩容；
- 清理 machine-id；
- 清理旧 SSH host key；
- 清理旧 cloud-init state；
- 明确默认用户。

---

## 7.8 磁盘与数据安全

### DISK-001 [P0] 根盘扩容

用户配置的根盘容量必须在 guest 首次启动后实际可用。

不能只修改 qcow2 虚拟容量而不扩展分区和文件系统。

### DISK-002 [P0] 数据盘稳定

数据盘在以下操作后不得被重建或丢失：

```text
stop -> start
restart
宿主机重启
Piglet 进程重启
```

### DISK-003 [P0] 禁止运行中修改

VM 运行时不得调用 `qemu-img` 修改其活动磁盘。

### DISK-004 [P0] 不得因重复执行清空磁盘

重复执行 `up`、`start` 或 reconcile，不得重建已有数据盘。

### DISK-005 [P0] 稳定设备标识

guest 侧挂载数据盘不应只依赖易变化的 `/dev/vdb` 顺序。

应尽量使用稳定 serial 或 `/dev/disk/by-id`。

### DISK-006 [P1] 数据盘保留策略

每块数据盘应能声明：

- 临时；
- 持久。

`destroy` 删除持久数据盘前必须进行额外确认或要求显式参数。

### DISK-007 [P1] 不支持静默缩盘

任何磁盘缩小请求必须拒绝。

扩大已有数据盘是否在 v1 支持，属于 P2。

---

## 7.9 生命周期与命令

### LIFE-001 [P0] 最小命令集

Piglet 至少需要：

```text
init
validate
doctor
up
start
stop
restart
status
list
ssh
exec
logs
destroy
version
```

### LIFE-002 [P0] Vagrant 心智映射

| Vagrant | Piglet |
|---|---|
| `vagrant up` | `piglet up` |
| `vagrant halt` | `piglet stop` |
| `vagrant status` | `piglet status` |
| `vagrant ssh` | `piglet ssh` |
| `vagrant destroy` | `piglet destroy` |
| `vagrant provision` | Pigsty/Ansible，不由 Piglet 重新发明 |

### LIFE-003 [P0] 幂等 up

- VM 不存在：创建并启动；
- VM 已停止：启动；
- VM 已运行且配置未变化：no-op；
- 配置发生可安全应用的变化：明确说明并应用或要求 restart；
- 配置发生不可安全应用的变化：要求 recreate，不能偷偷销毁。

### LIFE-004 [P0] 优雅停止

`stop` 应先请求 guest 正常关机，超时后再逐级升级，不得默认直接 kill。

### LIFE-005 [P0] 真实状态优先

`status` 必须以实际 QEMU 进程或控制通道状态为准，不能只相信旧 state 文件。

### LIFE-006 [P0] destroy 安全

`destroy`：

- 默认要求确认；
- 只删除当前项目拥有的实例资源；
- 默认不删除共享基础镜像缓存；
- 不得通过路径错误删除项目目录之外的文件。

### LIFE-007 [P1] recreate

应提供明确的 recreate 操作，用于镜像、架构、网络模式等不可变字段变化。

### LIFE-008 [P1] SSH 配置导出

Piglet 应能输出标准 OpenSSH config，使 Ansible、scp、VS Code Remote 等工具直接复用。

---

## 7.10 配置文件

### CONF-001 [P0] 声明式配置

Piglet 必须使用一个人类可读、可版本控制的配置文件定义环境。

推荐格式为 YAML，默认文件名可以是：

```text
piglet.yaml
```

### CONF-002 [P0] 单一事实来源

节点名称、IP、CPU、内存、镜像与磁盘不能分别散落在：

- shell；
- Makefile；
- 状态文件；
- 多份硬编码模板。

### CONF-003 [P0] 严格校验

必须拒绝：

- 未知字段；
- 重复节点名；
- 重复 IP；
- 重复宿主机端口；
- IP 不在 CIDR；
- CPU、内存或磁盘非法；
- 多节点使用不支持互通的网络模式；
- 非原生架构；
- 根盘小于基础镜像要求；
- 数据盘重名；
- 非绝对挂载路径。

### CONF-004 [P0] 默认值与节点覆盖

配置应支持：

- 全局 defaults；
- 单节点覆盖；
- profile；
- 命令行临时覆盖镜像或资源规模。

### CONF-005 [P1] 配置 Schema

应提供 JSON Schema 或等价机器可读 schema，便于编辑器校验和 Codex 自动修改。

### CONF-006 [P1] 当前规格迁移

Pigsty 当前 `vagrant/spec/*.rb` 应迁移为 Piglet profiles，而不是继续让 Ruby spec 成为长期事实来源。

---

## 7.11 Provisioning 与 Pigsty 集成

### PROV-001 [P0] Piglet 不重写 Ansible

Piglet 负责把 VM 拉到：

```text
可启动
网络可用
SSH 可用
磁盘可用
```

Pigsty 安装与业务配置继续使用现有 Ansible。

### PROV-002 [P0] 自动注入 SSH key

Piglet 必须自动完成宿主机到 VM 的 SSH 登录配置。

### PROV-003 [P0] 节点间 SSH

在需要由 `meta` 节点管理其他节点的 profile 中，应自动准备 `meta` 到其他节点的 SSH 能力。

### PROV-004 [P0] hostname 与 hosts

所有节点应拥有确定 hostname，并知道同一环境中的其他节点名称与 IP。

### PROV-005 [P0] 数据盘初始化

Piglet 或其生成的初始化配置必须完成：

- 发现指定数据盘；
- 格式化；
- 创建挂载目录；
- 写入稳定 fstab；
- 挂载；
- 重复执行安全。

### PROV-006 [P1] readiness

`piglet up` 应能够等待：

- QEMU 已运行；
- SSH 可用；
- guest 初始化完成。

是否进一步等待 Pigsty、Grafana 或 PostgreSQL 健康，属于 Pigsty profile 层，而不是 Piglet core 的 P0 能力。

---

## 7.12 诊断与可观测性

### DIAG-001 [P0] doctor

`piglet doctor` 必须检查：

- OS 与架构；
- QEMU binary；
- `qemu-img`；
- HVF 或 KVM；
- Linux `/dev/kvm` 权限；
- 固件；
- 镜像缓存权限；
- 磁盘空间；
- SSH；
- 网络辅助组件；
- 当前配置冲突。

### DIAG-002 [P0] 可执行修复建议

错误不能只写“启动失败”。

应说明：

- 缺什么；
- 检测到什么；
- 应安装哪个包或执行哪个命令；
- 日志在哪里。

### DIAG-003 [P0] 每节点日志

至少保留：

```text
QEMU stderr/stdout
serial console
生命周期事件
最终 QEMU argv
```

### DIAG-004 [P1] debug bundle

应能生成脱敏诊断包，包含：

- 配置；
- 状态；
- doctor；
- QEMU 能力；
- 网络状态；
- 日志。

不得包含 SSH private key 与密码。

### DIAG-005 [P1] JSON 输出

`doctor`、`status`、`list` 应支持机器可读输出，便于 Pig、CI 与其他工具调用。

---

## 7.13 状态、并发与恢复

### STATE-001 [P0] 项目隔离

不同 Piglet 项目必须拥有独立的：

- 状态；
- 磁盘；
- socket；
- 日志；
- SSH key。

### STATE-002 [P0] 排他锁

同一个项目不能同时执行两个可能修改状态的操作。

### STATE-003 [P0] 原子写入

状态文件更新必须采用原子写入。

不能在进程崩溃时留下半截 JSON 或 YAML。

### STATE-004 [P0] stale 状态恢复

Piglet 必须识别：

- PID 文件存在但进程已死；
- socket 存在但 QEMU 不可达；
- QEMU 在运行但 state 不完整；
- 创建中断留下的临时文件。

### STATE-005 [P0] 配置漂移

Piglet 应保存实际运行规格的摘要，并能指出当前配置与实例状态的字段差异。

### STATE-006 [P1] 状态版本迁移

状态 schema 必须带版本，为将来升级保留迁移入口。

---

## 7.14 安装与分发体验

### INSTALL-001 [P0] 单一 Piglet 二进制

Piglet 本体应以单一 Go 二进制分发，不依赖：

- Ruby；
- Python runtime；
- cgo libvirt binding。

### INSTALL-002 [P0] 外部运行时只有 QEMU

基础宿主机依赖应尽量收敛为：

```text
Piglet
QEMU system binary
qemu-img
必要固件
```

### INSTALL-003 [P0] 不要求安装旧组件

Piglet 主路径不得要求用户安装：

- Vagrant；
- VirtualBox；
- vagrant-libvirt；
- libvirt daemon；
- Lima。

### INSTALL-004 [P0] 平台包管理器

正式发布至少应提供清晰安装路径：

- macOS Homebrew；
- Linux tarball；
- P1：RPM 和 DEB。

### INSTALL-005 [P0] 网络辅助组件分离

若 private 网络需要特权 helper：

- helper 安装必须与普通 Piglet 二进制安装分离；
- 用户模式或单节点模式不应无条件依赖 helper；
- 安装内容与权限必须透明。

### INSTALL-006 [P1] 卸载干净

Piglet 应能列出并卸载自己创建的网络辅助资源。

不得留下未知：

- bridge；
- launchd 服务；
- systemd 服务；
- root 文件。

---

# 8. 非功能需求

## 8.1 性能

### NFR-PERF-001 [P0] 使用原生硬件加速

- macOS 使用 HVF；
- Linux 使用 KVM；
- 原生架构使用 host CPU model 或等价能力。

### NFR-PERF-002 [P0] 避免完整磁盘复制

多 VM 必须复用只读基础镜像并使用 COW overlay。

### NFR-PERF-003 [P1] 多节点并行

四节点环境不应因为工具层完全串行而显著拖慢。

20 节点环境应有资源保护与并发上限。

### NFR-PERF-004 [P0] 不夸大性能语义

Piglet 的目标是本地开发与功能测试。

macOS 上的数据库 I/O 结果不应被包装成与 Linux KVM 或裸机完全等价的生产性能结论。

---

## 8.2 易用性

### NFR-UX-001 [P0] 默认命令短而稳定

常用流程不应要求用户手工拼 QEMU 参数或编辑系统网络。

### NFR-UX-002 [P0] 错误直接

错误信息应直接指出：

- 缺少的包；
- 权限问题；
- 端口冲突；
- 镜像问题；
- 网络问题。

### NFR-UX-003 [P0] 同平台同语义

同一个命令在 macOS 与 Linux 上应有相同高层含义，即使底层网络实现不同。

---

## 8.3 可靠性

### NFR-REL-001 [P0] 不丢盘

生命周期命令不得清空或重建已有数据盘。

### NFR-REL-002 [P0] 可重复

单节点与四节点环境至少要能连续执行多轮：

```text
create -> up -> stop -> start -> destroy
```

不得残留：

- 僵尸进程；
- socket；
- 锁；
- 网络资源。

### NFR-REL-003 [P1] 30 次循环验收

正式替代 Vagrant 前，Tier 1 平台需要通过至少 30 次连续生命周期循环。

---

## 8.4 安全

### NFR-SEC-001 [P0] QEMU 普通用户运行

完整 QEMU 进程不得使用 root。

### NFR-SEC-002 [P0] 下载校验

镜像与特权 helper 必须校验来源与摘要。

### NFR-SEC-003 [P0] 路径边界

任何 destroy 或 prune 操作都必须确认目标在 Piglet 管理目录内。

### NFR-SEC-004 [P0] 控制 socket 权限

QEMU 控制 socket 与 SSH private key 只能由当前用户访问。

### NFR-SEC-005 [P0] 可信镜像边界

Piglet 只承诺运行 Pigsty 官方或用户信任的 Linux 镜像，不承诺强隔离恶意 guest。

---

## 8.5 可维护性

### NFR-MAINT-001 [P0] 范围克制

不得为了“以后也许有用”引入：

- 通用 provider；
- 插件系统；
- 全量 QEMU 抽象。

### NFR-MAINT-002 [P0] 能力探测优先

不得只依赖版本号猜测 QEMU 能力。

应探测：

- accelerator；
- machine；
- device；
- netdev；
- firmware。

### NFR-MAINT-003 [P0] 测试可重复

以下模块应尽量成为可单元测试的纯逻辑：

- 命令构造；
- 配置验证；
- 镜像解析；
- 状态机；
- 网络计划；
- guest 初始化配置生成。

---

# 9. 当前技术决策与可替换边界

本节记录已经形成共识或高度倾向的实现方向。

除标记为硬约束的项目外，实现者可以提出等价替代方案，但必须满足前述产品需求。

## 9.1 已确认硬约束

1. 使用 Go 实现；
2. QEMU 是唯一 VM backend；
3. macOS 使用 HVF，Linux 使用 KVM；
4. 只正式支持原生架构；
5. 不依赖 Vagrant、VirtualBox、libvirt 或 Lima；
6. 完整 QEMU 不以 root 运行；
7. Piglet 不是通用 Vagrant 重写。

## 9.2 当前推荐实现

以下属于当前推荐，而不是不可替代的产品语义：

- qcow2 作为唯一正式磁盘格式；
- cloud-init NoCloud 负责首次启动初始化；
- QMP 负责查询与控制生命周期；
- macOS private 网络使用 `socket_vmnet`；
- Linux private 网络使用 bridge/TAP 或 `qemu-bridge-helper`；
- 单节点 portable 模式使用 QEMU user-mode NAT 与 host forwarding；
- 系统 OpenSSH 作为 SSH 客户端；
- XDG 目录保存缓存与状态。

实现者若要改变这些选择，必须给出：

- 对应需求仍被完整满足的证明；
- 跨平台行为；
- 安装与权限影响；
- 失败恢复；
- 测试方案。

---

# 10. 配置示例

以下示例表达需求，不代表最终 schema 已经锁死。

## 10.1 单节点

```yaml
version: 1
name: meta

network:
  mode: private
  cidr: 10.10.10.0/24
  gateway: 10.10.10.1

nodes:
  - name: meta
    address: 10.10.10.10
    image: u24
    cpus: 4
    memory: 8GiB
    root_disk: 64GiB
    disks:
      - name: data
        size: 128GiB
        mount: /data
```

## 10.2 四节点 full

```yaml
version: 1
name: full

network:
  mode: private
  cidr: 10.10.10.0/24
  gateway: 10.10.10.1

defaults:
  image: u24
  cpus: 1
  memory: 2GiB
  root_disk: 64GiB

nodes:
  - name: meta
    address: 10.10.10.10
    cpus: 2
    memory: 4GiB

  - name: node-1
    address: 10.10.10.11

  - name: node-2
    address: 10.10.10.12

  - name: node-3
    address: 10.10.10.13
```

## 10.3 MinIO/Silo 多盘节点

```yaml
version: 1
name: minio

network:
  mode: private
  cidr: 10.10.10.0/24
  gateway: 10.10.10.1

nodes:
  - name: minio-1
    address: 10.10.10.21
    image: u24
    cpus: 2
    memory: 4GiB
    root_disk: 64GiB
    disks:
      - { name: data1, size: 32GiB, mount: /data1 }
      - { name: data2, size: 32GiB, mount: /data2 }
      - { name: data3, size: 32GiB, mount: /data3 }
      - { name: data4, size: 32GiB, mount: /data4 }
```

---

# 11. Pigsty 集成与迁移需求

### MIG-001 [P0] 新旧路径并存

Piglet 开发期间，不得立即删除现有 Vagrant、VirtualBox 和 libvirt 路径。

### MIG-002 [P0] 显式回退

迁移期应能显式选择：

```text
Piglet
Vagrant + VirtualBox
Vagrant + libvirt
```

回退是销毁后重建，不要求运行中无缝迁移。

### MIG-003 [P1] profile 等价

Piglet profiles 必须能够表达当前 `vagrant/spec/*.rb` 的资源与拓扑语义。

### MIG-004 [P1] 旧环境变量适配

Pigsty wrapper 可以继续接受现有常用变量：

```text
VM_SPEC
VM_IMAGE
VM_SCALE
VM_ARCH
```

但兼容逻辑不应污染 Piglet 的核心配置模型。

### MIG-005 [P1] Makefile 入口

Pigsty 当前的：

```bash
make meta
make full
make minio
make simu
```

应能逐步切换为调用 Piglet，而无需用户学习复杂的新命令。

### MIG-006 [P0] 删除旧路径的条件

只有满足以下条件后，才可以把 Piglet 设为唯一默认实现或删除旧 provider：

- macOS ARM64 与 Linux AMD64 真实 E2E；
- 8 × 2 镜像矩阵达到约定支持级别；
- `meta`、`full`、`minio`、`simu` 通过；
- 固定 IP、VM 互通、宿主机访问与出网通过；
- 磁盘持久性通过；
- 连续生命周期测试通过；
- 安装、doctor、故障排查文档完成；
- 至少保留一个 Pigsty 发布周期的回退观察期。

---

# 12. 验收标准

## 12.1 单节点 P0 验收

在 Apple Silicon macOS 与 Linux AMD64 上，使用同一份逻辑配置：

```bash
piglet up -f profiles/meta.yaml
```

必须满足：

1. VM 使用宿主机原生架构镜像；
2. 使用 HVF 或 KVM；
3. CPU 与内存符合配置；
4. 根盘扩容生效；
5. 数据盘正确挂载；
6. `piglet ssh meta` 可用；
7. VM 可访问互联网；
8. 宿主机有稳定方式访问 PostgreSQL 和 Web 服务；
9. stop/start 后数据不丢；
10. destroy 不删除基础镜像缓存；
11. QEMU 非 root；
12. 不需要安装 Vagrant、VirtualBox、libvirt 或 Lima。

## 12.2 四节点 P0/P1 验收

```bash
piglet up -f profiles/full.yaml
```

必须得到：

```text
meta    10.10.10.10
node-1  10.10.10.11
node-2  10.10.10.12
node-3  10.10.10.13
```

并满足：

1. 四台 VM 均可启动；
2. VM 之间通过固定 IP 双向互通；
3. 宿主机至少可直接访问 `meta`；
4. private 完整模式下宿主机可访问全部节点；
5. 访问 `10.10.10.0/24` 走私网；
6. 默认路由仍用于互联网；
7. meta 可 SSH 到其他节点；
8. stop/start 后 IP 与磁盘不变；
9. 部分节点失败时保留成功节点与日志。

## 12.3 多盘验收

MinIO/Silo 节点必须看到四块独立数据盘，并稳定挂载到：

```text
/data1
/data2
/data3
/data4
```

在 stop/start 和 Piglet 重启后，盘内测试数据仍存在。

## 12.4 兼容性验收

至少对以下系统完成安装 smoke：

```text
EL8
EL9
EL10
Debian 12
Debian 13
Ubuntu 22.04
Ubuntu 24.04
Ubuntu 26.04
```

Tier 1 平台必须真实验证，不能用 mock 代替。

## 12.5 稳定性验收

至少执行 30 次：

```text
up -> status -> stop -> start -> destroy
```

不得残留：

- QEMU 进程；
- QMP socket；
- 锁；
- 临时下载；
- 不受控 TAP 或 bridge；
- 被意外清空的数据盘。

---

# 13. 建议交付阶段

本节是建议计划，不是强制版本号。

## Phase 0：技术验证

只验证：

- Go 启动 QEMU；
- macOS ARM64/HVF；
- Linux AMD64/KVM；
- qcow2 overlay；
- 基础初始化；
- SSH；
- 正常关机；
- 不依赖 Vagrant、libvirt 或 VirtualBox。

## Phase 1：单节点 MVP

完成：

- 配置；
- 镜像缓存；
- 磁盘；
- 生命周期；
- doctor；
- 日志；
- 单节点网络；
- `meta` profile。

## Phase 2：固定私网与多节点

完成：

- host ↔ VM；
- VM ↔ VM；
- 固定 IP；
- `full`；
- `minio`；
- 多节点并行；
- private 网络安装。

## Phase 3：Pigsty 全量迁移

完成：

- 所有主要 profiles；
- 架构与镜像矩阵；
- Makefile 集成；
- 旧变量 wrapper；
- 现有 Vagrant 对照验证。

## Phase 4：默认切换

完成：

- 安装包；
- Homebrew、RPM、DEB；
- 自托管 E2E；
- 30 次稳定性测试；
- 文档；
- 回退观察期。

---

# 14. 明确非目标

以下能力不属于当前 Piglet 需求，除非另立项目或新的需求版本：

```text
Vagrantfile 兼容解析
通用 provider/plugin 系统
VirtualBox/VMware/libvirt backend
Windows host
Windows/macOS guest
生产级多租户隔离
远程 hypervisor
云厂商资源
OpenTofu/Terraform provider
GUI
共享目录/virtiofs
快照
在线迁移
热插拔
GPU/USB/PCI 直通
任意 VLAN/OVN/SDN
默认跨架构 TCG
自动替代 Pigsty Ansible
```

其中某些能力未来可能有价值，但不得拖累首个可用版本。

---

# 15. 待产品负责人确认的集中问题

以下问题尚未在原始讨论中得到明确最终答案，应集中确认，而不是由 Codex 自行猜测。

## Q1：单节点默认网络

默认 `piglet up` 是：

- A. 直接提供宿主机可访问的固定 IP；
- B. 默认零特权 NAT + 自动端口转发，`--network private` 才启用固定 IP。

当前建议：**B 作为默认，A/private 作为完整模式**，但需要负责人确认。

## Q2：Piglet 与 Pig 的关系

- A. 独立仓库与独立 `piglet` 二进制；
- B. 直接实现为 `pig vm`；
- C. 独立 core，由 `pig vm` 包装。

当前建议：**C**。

## Q3：Piglet 是否自动安装 Pigsty

- A. 只管理 VM，用户或 Pigsty 自己运行 Ansible；
- B. 提供 `piglet up --install-pigsty`；
- C. profile 可以定义 readiness 或 bootstrap，但 core 不认识 Pigsty。

当前建议：**C**。自动安装属于 profile 或 Pig wrapper。

## Q4：destroy 的默认数据盘策略

- A. 所有数据盘随 VM 删除；
- B. 默认保留数据盘；
- C. 每块盘显式声明 `persistent`，默认临时。

当前建议：**C**。

## Q5：首个正式支持的 macOS x86 与 Linux ARM64

需要确认它们是：

- v1 GA 必须；
- v1.1；
- best effort。

当前建议：

- macOS ARM64 + Linux AMD64 为 Tier 1；
- macOS AMD64 + Linux ARM64 为 Tier 2。

## Q6：镜像仓库与命名

需要确认：

- 镜像托管域名；
- manifest 仓库；
- 是否沿用 `cloud-image/*` 命名；
- 发布通用 cloud image，还是 Pigsty 定制 image。

## Q7：是否允许显式跨架构实验模式

当前需求明确“不需要跨架构”。

建议 v1 直接拒绝。未来如加入，应显式标注实验，且不进入正式验收矩阵。

---

# 16. 给实现者的边界说明

1. 不要把 Piglet 做成另一个 Vagrant；
2. 不要先写通用抽象，再寻找 Pigsty 用例；
3. 先完成单节点真实 VM，再做多节点私网；
4. 单元测试不能替代真实 HVF/KVM E2E；
5. 不得以“理论上可行”宣布发行版或平台支持；
6. 数据盘安全优先于方便；
7. provider 差异可以隐藏，但宿主机能力差异不能伪装不存在；
8. 配置、状态、镜像与运行时必须有清晰边界；
9. 所有特权操作必须最小化、显式化、可卸载；
10. 未经产品负责人确认，不要实现第 14 节的非目标能力。

---

# 17. 需求基线结论

Piglet 的本质不是“用 Go 重写 Vagrant”，而是：

> 为 Pigsty 提供一个范围克制、跨 macOS/Linux、统一使用 QEMU、能够稳定管理单节点与多节点 Linux 虚拟机实验室的本地运行时。

只要 Piglet 能可靠完成：

```text
镜像
CPU/内存
根盘/数据盘
固定 IP
VM 互通
宿主机访问
出网
SSH
生命周期
诊断
```

它就已经覆盖 Pigsty 当前对 Vagrant 的绝大多数真实依赖。

其他通用虚拟化能力不应进入首版范围。