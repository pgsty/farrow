# Farrow 0.8 使用体验审计

审计开始：2026-09-20。源码基线 `436ebe2`，本机已安装 CLI 为 Homebrew
0.7.0 (`9c6d489`)。本表针对当前工作区；初始未提交修改已另存补丁，
不把源码、测试、安装、发布视作同一证据。没有 mini pc 官方链接，尚未做对标。

证据状态仅使用：当前已复现、代码已确认、历史记录待复现、待验证假设。
回归桩复现与原生 VM 复现另行注明。规模 S 为局部修复，M 为跨模块改动，L 为设计项。

## 按优先级排列的任务表

| ID / 优先级 / 交付状态 | 用户场景 | 证据状态与复现方法 | 根因 | 资源归属 | 最小改动 | 用户可见行为 | 验收方法 | 规模 / 必要依赖 |
|---|---|---|---|---|---|---|---|---|
| U16 / P1 / 已完成源码修复 | macOS 首次 setup 安装 Homebrew socket_vmnet 后，已输入密码仍报需要认证 | 当前已复现：回归模拟 Homebrew 清除 sudo timestamp，旧顺序在特权安装时报需要密码；源码顺序与本机 Homebrew 行为吻合，修复后回归通过 | 在 Homebrew 发现/安装之前认证，后续 brew 普通路径主动使凭据失效 | Homebrew 用户态来源与既有 root-owned Farrow 网络计划；不改 sudoers | 用户态发现/安装/下载结束后才认证，紧接原网络安装；repair/Linux 保留原顺序 | 一次认证可用于实际安装；下载失败或取消先返回根因，不申请无用认证 | 新 formula/慢 prefix 清凭据回归、已有 formula、坏归档/回退/取消、认证失败、repair/healthy 边界；补丁原生安装另验 | S；真实交互认证由专用 Mac 验收，不在回归中执行 sudo/brew |
| U10 / P1 / 诊断已完成，macOS 共享支持待设计 | 源目录恢复后 macOS 节点仍无法启动 | 当前已复现：QEMU 11.1.1 对 `/dev/fd/3` 的 open(O_DIRECTORY) 返回 ENOTDIR；实际路径和权限正常 | 现有 FD 安全绑定与 Darwin 目录重开语义不兼容，device-help 探测未覆盖 | 用户宿主项目树；不得降级路径身份校验 | 已补逐节点启动前诊断及 restart/reload/recreate 的停止前检查；完整修复需可验证的 QEMU FD 接口 | 明确源/挂载点与平台能力缺口，保留其他节点和现有磁盘；不输出无效的盲重试或自动 recreate | 原生错误前后对比、Darwin 回归、停止前状态不变；完整共享仍是发布范围决策/验收缺口 | S 诊断已做 / M-L 完整支持；QEMU 平台接口与 Linux 对照 |
| U01 / P1 / 已完成节点隔离 | 一个宿主共享目录缺失，其他节点也无法 up/start | 当前已复现：0.7.0 原生双节点时整批退出 1；修复后退出 5，无共享的运行节点仍 ready；start 案例也通过 | 节点前置检查被提升为全局依赖 | 宿主目录属于用户；QEMU 文件描述符和节点状态属于 Farrow | 将创建前校验移到单节点 prepare；已有节点使用逐节点 start preflight；保留 restart/reload/recreate 在停止前的保护 | 可用节点继续；受影响节点列出源路径、挂载点和恢复动作；不创建空目录 | 双节点部分成功、恢复目录后重试、运行节点不重启、未知/不安全路径仍拒绝；真实 VM | S；挂载级省略 QEMU share 另见 U08 |
| U04 / P1 / 已完成限定分支 | 一台新节点准备失败，选中的已有停止节点未启动 | 当前已复现：原生已有停止节点 + 新节点缺共享；修复后已有节点就绪，新节点单独失败；仅覆盖 controller 的 prepare/start 局部失败，见 U14 | 新建分支与已有节点启动串行耦合 | 已提交节点身份与磁盘已知 | 仅部分节点失败时继续独立已有节点；合并结果和失败 | 成功节点保持成功，未验证运行节点不报 ready | 混合新建/已有节点故障、取消与完整性错误边界 | M；复用现有 partial 结果 |
| U02 / P1 / 已完成 | 已有 VM 丢失派生公钥，up 的 Guest 恢复失败 | 当前已复现：0.7 原生 VM 的宿主 `.pub` 删除后 up 自动补回，原私钥摘要、VM UUID、根盘 inode 不变；修复前回归失败 | 已有公钥恢复未接入已有 VM 路径 | 已验证的部署原私钥；公钥为派生物 | 原启动路径复用安全密钥检查；已有 VM 禁止缺失私钥时生成新身份 | 恢复公钥并继续；缺原私钥明确备份恢复 | 对已有 VM 的回归测试、原私钥字节不变、私钥丢失拒绝 | S；接续既有修复 |
| U15 / P1 / 已完成 | 删除带持久盘的单个节点后，整批 destroy / purge 被该保留盘阻断 | 当前已复现：原生 destroy c 后销毁 a/b 报 retained disk c/data not present；回归先失败后通过 | 销毁路径误用新建配置的完整 desired-set 校验 | 严格 Inventory 已确认的 Farrow 保留盘；未知文件仍拒绝 | 仅销毁校验允许已移出规格的节点保留盘，仍核验在规格中的精确身份；新建/重新挂载校验不变 | 普通 destroy 保留旧节点数据，后续显式删除/purge 可继续 | 整个 DestroyNodes→Destroy→DeletePersistent 回归；未知文件阻断删除；原生清理及 0.7 旧路径迁移 inode 不变 | S；不扩大自动删除范围 |
| U03 / P1 / 已完成 | destroy --delete-persistent / purge 输出自相矛盾 | 当前已复现：回归覆盖原先保留/删除文案冲突；原生显式删除 1 块持久盘、purge 后路径实查通过 | 用中间阶段文本累积最终结果 | 已确认归属的节点、持久盘、密钥、缓存 | 在各删除步骤成功后重写最终摘要；失败保留实际阶段 | 只汇总最终保留/删除事实，文本和 JSON 一致 | 普通 destroy、删除持久盘、purge、清理失败回归与磁盘实查 | S |
| U13 / P1 / 已完成限制诊断 | 控制节点原 Guest key 消失，up 仍显示健康且无警告 | 当前已复现：受控移走 `/home/dba/.ssh/id_ed25519` 后旧输出无 warnings；回归先失败，修复后原生出现 control-ssh 限制 | up 信任之前 ready marker，未验证当前控制节点密钥文件 | Farrow 已认证 Guest 中的托管 key；不生成、不传输新私钥 | 复用现有有期限的 control-ssh helper 检查；失败仅重试该阶段并保存限制 | 管理 SSH ready/退出 0，节点间 SSH 限制明确；恢复原文件后 up 清除限制；不建议盲重试 | 原生移走/恢复 + 节点间 SSH，回归断言、非控制节点/start 不增加检查 | S；安全原位注入另见 U05 |
| U06 / P1 / 已完成阶段关联 | 自动 setup 重试的操作编号改变；首次失败无 logs | 当前已复现：自动化回归覆盖父操作与 setup/两次尝试同 ID、首次 setup 失败无部署日志、早期错误 ID 与 dry-run 无写入 | 操作边界放在内层；事件日志依赖部署 | Farrow 用户目录、现有事件日志 | 外层复用操作编号；无部署也写入同一受限日志路径 | 一次命令及恢复可追踪，脚本不被日志污染 | setup 失败/成功重试、取消、无部署 logs、大小限制与脱敏 | M；不引入新诊断系统 |
| U11 / P1 / 已完成局部重试提示 | 部分失败/Ctrl-C 后复制重试命令，私有仓库或 no-wait/rollback 丢失；start 被指向 up | 当前已复现：原函数只带 -f；新增回归验证引号、repo、适用 flags 和 start 语义 | 输出层缺少原调用上下文 | 只生成指引，不改资源 | 输出上下文携带已解析 repo 和操作选项；start/restart 的继续动作为 start | 不静默换源，作用域保持失败节点；Guest 修复明确需要等待 | CLI 文本回归、原生 Ctrl-C 后继续；自动 setup 本身仍复用原调用 | S；脚本宿主准备分支另见 U12 |
| V01 / P1 / 回归验证通过 | vmnet 日志权限、自身网段误报、start/restart/reload 准备 | 代码已确认，工作区已有修复和测试；运行对应测试、doctor 和隔离生命周期 | 已有修复，非新发现 | root-owned Farrow 服务/日志及网络计划 | 验证归属、真实冲突、一次恢复、保留上下文 | 可恢复则继续，真实外部冲突仍拒绝 | Darwin/网络/CLI 测试；不重启用户网络服务 | S 验证；管理员认证按现有 helper |
| V02 / P1 / 回归验证通过 | image info 不应需要未安装 QEMU；本地镜像损坏原因 | 代码已确认，工作区已有修复；空 PATH/损坏本地镜像测试 | 已有修复，非新发现 | 已签名目录、注册镜像和被引用缓存 | 验证真实错误与 integrity/7，不替换运行 VM 镜像 | 信息查询可离线；摘要不符仍拒绝 | 图像与 CLI 回归；私有 repo 不静默改源 | S 验证 |
| V03 / P1 / 回归验证通过 | 缺公钥、私钥丢失、网络后端下载中断 | 代码已确认，工作区已有恢复/重试测试 | 已有修复，非新发现；集成缺口见 U02 | 部署原密钥、下载临时文件 | 验证有限重试、取消清理、身份保留 | 自动恢复派生物，不能恢复时给出真实原因 | sshkeys/setup 回归、三次上限与校验失败 | S 验证 |
| U05 / P1 / 后续设计 | 0.6/0.7 控制节点暂存或安装 SSH 密钥丢失 | 当前已复现：隔离控制节点将原 Guest key 暂时移走，宿主管理 SSH 可用但节点间 SSH 无钥；原位注入通道尚缺，诊断修复见 U13 | 旧初始化消费暂存 key 后无原位补入通道 | 原部署密钥、SSH 主机身份、VM UUID 必须匹配；其他密钥不可覆盖 | 认证后以 stdin 传输原密钥，验证目标身份和既有文件；单次安装后复验 | up 原位恢复节点间 SSH，保留 VM/磁盘；身份不明继续明确失败 | 密钥缺失/不匹配/软链、非控制节点、日志无密钥、真实节点间连接 | M；安全输入通道与故障验证 |
| U07 / P1 / 待复现 | 首选 DNS 超时，宿主备用可用但 Guest 解析失败 | 历史记录待复现：UX-PLAN 2026-09-15；隔离 DNS 首选丢包、备用成功 | 待核实 QEMU DNS 转发与宿主 resolver 选择 | DNS 可含企业/VPN 规则；不能替换成公共 DNS | 推荐先显式 DNS 配置与诊断；自动回退只用同作用域、实测可用 resolver | DNS/路由/Guest 出网/SSH 分开显示 | split DNS、VPN、内网域名、无公网环境与故障注入 | M/L；隔离 DNS/专用主机 |
| U12 / P1 / 待后续实现 | 非交互式缺宿主依赖，从已应用状态恢复时得到泛化 setup 提示 | 代码已确认：`runLifecycleCommand` 的交互分支可传内部 Applied，公开 setup 在无 inventory 时仍按模板选择；非交互错误未统一带完整下一步 | 公开 setup 的配置来源契约与生命周期不同 | 已应用配置不可被模板/当前目录无关文件替换 | 推荐先让无显式配置的 setup 有明确、可测试的 applied-state 路径，再生成 setup + 原命令指引；不新增 repair | 脚本立即失败且命令可复制；默认模板、显式 -f、已有部署的优先级不含糊 | 管道/非 TTY；自定义 CIDR、repo、selector；无隐藏 sudo 等待、无新配置覆写 | M；配置来源兼容性测试和帮助更新 |
| U14 / P1 / 后续拆分 | 新节点镜像解析/下载失败，已有停止节点仍被全局前置阶段挡住 | 代码已确认：`Manager.Up` 的 resolveBases 在 CreateAndStart 前整批返回；本轮未进行私有仓库原生故障注入 | 新建资源准备与已有磁盘启动仍共享部分全局前置阶段 | 已有 VM 可独立证明身份；镜像摘要/来源仍须严格核验 | 推荐按实际需要分离新建节点的镜像与共享设备能力检查，保留必要全局网络/状态锁边界 | 现有节点可启动，镜像失败保留原源/版本和根因 | 无网络已有磁盘 + 新建坏镜像、私有 repo、取消、全局身份错误不能继续 | M；不把 U04 的局部修复宣称覆盖此分支 |
| V04 / P1 / 部分完成，外部条件待齐 | 安装→首次 SSH、旧 VM 升级、宿主重启恢复、PATH/helper 配对 | 当前已复现：本机隔离数据目录、0.7 首次 SSH、源码接续、真实 Ansible ping、SIGINT 恢复；干净宿主安装/Linux/reboot 仍未验证 | 非已确认缺陷，发布验收缺口 | 隔离部署；主机重启仅专用测试机 | 本机隔离源码 VM；现有 installer 测试；原生 Linux 与重启有环境才执行 | 文档、安装版本和实际使用一致 | macOS arm64/Linux amd64 原生；0.7 保留磁盘；Pigsty 实际连接 | M；干净测试机/明确可重启主机 |
| U08 / P2 / 后续设计 | 同一 VM 多个 share 之一缺失；既有嵌套目录 UID 不匹配 | 代码已确认 share FD 按完整配置绑定；UID 问题为历史记录待复现 | 运行 invocation 与期望 share 列表强绑定；完整 UID 映射缺失 | 宿主项目树属于用户，禁止递归更改 | 推荐先逐节点隔离和准确挂载诊断；挂载级省略需可恢复的 FD/状态事务，完整映射另立设计 | 当前不掩盖错误；后续可降级而保留可用挂载 | 多 share/FD 身份/恢复重启、已有嵌套文件写入与只读回退 | L；不扩大 0.8 首批范围 |
| U09 / P2 / 测量完成，无性能改动 | 下载、冷启动、已有磁盘启动和健康重复 up 等待 | 当前已复现：本轮下载、冷启动、已有磁盘启动及 20 次 warm 分开计时；旧 snapd 冷启动 44.349s 仍属历史记录待复现 | 本轮 warm 无明显等待，旧冷启动 snapd 瓶颈尚未证实 | 隔离实验 VM 和镜像 | 记录分阶段时长和至少 20 次 warm 样本；仅优化已测瓶颈 | 不承诺未经测量的启动时间 | 冷/暖路径分开，p50/p95，UUID/PID/磁盘保留 | S 测量 / M 镜像改动；原生环境 |

## 必须保留的边界

单部署模型；`up` 收敛 Guest、`start` 只启动已有节点；不新增 repair 命令。
归属/身份/摘要不明不自动重建。设备缺失、探测失败、忙碌挂载和宿主 I/O 错误
不授权格式化；根盘和宿主共享目录不进入实验数据盘重置范围。
源目录不自动 mkdir，不递归 chmod/chown。脚本不隐式等待 sudo/确认。
已有成功 VM、磁盘和未提交修改保留。推送、tag、发布不在本轮授权内。

## 验收记录

证据目录：`/Users/vonng/.codex/visualizations/2026/09/20/01a0be85-53e8-7320-91c1-1efaa16816b0/farrow-0.8-ux`。
`native-runs.jsonl` 逐项记录命令、实际二进制路径、退出码和墙钟时长；对应 stdout/stderr
分开保存。初始补丁及未跟踪文件摘要留在该目录。私钥内容不放入报告或命令日志。

### 已完成修复的位置和回归

| 改动 | 代码入口 | 回归 / 执行证据 |
|---|---|---|
| U16 macOS 来源准备后认证 | `cmd/farrow/setup.go:installSetupDarwinNetwork` | `setup_auth_test.go`；`/Users/vonng/pgsty/repo/data/build/farrow-0.8.0-20260921/setup-auth-before.log` 失败 → `setup-auth-after.log` 通过；使用真实来源选择、fake Homebrew/root runner，不作为真实 VM 或新版安装证据 |
| U01 共享源逐节点隔离 | `internal/private/prepare.go:PrepareNode`、`manager.go:Up/startExisting`、`internal/hostshare/hostshare.go:Open` | `share_recovery_test.go`；`native-070-missing-share` 与 `native-source-missing-share`、`native-source-start-missing-share` |
| U02 既有 VM 公钥恢复 | `internal/sshkeys/keys.go:EnsureExistingKeys`、`internal/private/manager.go:ensureKeys/startExisting` | `key_recovery_test.go`、`sshkeys/recovery_test.go`；`native-source-public-recovery`，SHA/UUID/inode 断言 |
| U15 保留盘不阻断后续清理 | `internal/private/persistent.go:validatePrivatePersistentState` | `retained_cleanup_test.go`，`retained-cleanup-before.log` 失败 → `retained-cleanup-after.log`；`native-final-destroy` |
| U03 清理最终事实 | `cmd/farrow/main.go:runPrivateCommand` | `TestPurgeDisposesAppliedDeploymentWithoutConfirmation`；隔离清理记录见下 |
| U04 部分 prepare/start 失败后继续已有节点 | `internal/private/manager.go:Up`、`controller.go:isolatedPartialError` | `TestUpAttemptsExistingPeersAfterNewNodePrepareFailure`、`TestPartialResultsDoNotHideGlobalFailures`；`native-source-mixed-failure` |
| U06 同一次操作关联与无部署日志 | `cmd/farrow/operation.go`、`main.go:runLifecycleCommand/runLogs`、`setup.go:runSetupCommand`、`manager.go:LogPath` | `operation_test.go` 四个回归；trace 不记录 setup 参数，8 MiB 上限/保留 4 MiB 沿用 diagnostics；详细根因仍在当次命令输出 |
| U10 Darwin 能力错误 | `hostshare.go:ValidateQEMUAccess`、`private/shares.go`、`manager.go:Reload/Restart`、`destroy.go:RecreateResolved` | `access_darwin_test.go`、`share_access_darwin_test.go`；`native-macos-share-before` 与 `native-source-share-capability-final` |
| U11 部分/取消重试与 SSH alias | `cmd/farrow/lifecycle_output.go`、`lifecycle_integrations.go`、`main.go` | `retry_context_test.go`、`lifecycle_integrations_test.go`；原生自动端口 12223 → 22223 后 SSH 成功 |
| U13 当前控制 key 限制 | `internal/private/start.go:NativeLifecycle.WaitReady`、`internal/cloudinit/render.go:renderControlSSHInstallScript` | `TestUpReportsDisappearedControlKeyWithoutFailingManagementSSH`，`control-key-before.log` 失败 → `control-key-after.log` 通过；`native-control-key-missing-before` / `native-control-key-missing` |

已有五项工作分别由 baseline 测试和最终 `make check` 验证，不列作本轮新发现。
修复继续使用现有状态、锁、Guest helper、SSH 和事件日志，没有新增修复命令。
对局部 PartialError 的继续执行有专门限制：取消、锁释放错误、身份或其他全局错误不得
被合并成可以忽略的节点错误。`--no-wait` 的原生部分结果没有 ready=true。

### 原生操作与数据边界

- 隔离 `FARROW_HOME` 和子进程 SSH home；节点只用 ux08-a/b/c/d、10.10.10.210–213。
  复用已健康的宿主网络，未重启/重装 socket_vmnet，未触碰用户四台 pg-* VM。
- 用实际 Homebrew 0.7.0 下载 u24:stable（解析为 u24@20260801.0.0）并首次启动 a，首次
  `exec id` 成功。随后用源码二进制操作相同 a；UUID/原私钥/根盘 inode 保留。
  后续 stop/start/reload 改变 PID 属于显式测试操作，健康重复 up 则要求 PID 也不变。
- b 的共享源恢复后暴露了独立的 Darwin QEMU 故障。实验性路径探查不计作修复，未保留
  任何弱化路径绑定的实现。为继续测试，明确移除测试 b 的共享后只重建该测试节点；
  这不能作为“原 VM 共享恢复成功”的证据，也未重建 0.7 创建的 a。
- 新 c 缺目录 + 已停止 a：a、b ready，c 单独失败，退出 5。修复目录的 prepare 回归通过；
  macOS 原生挂载恢复仍被 U10 阻断，Linux 原生挂载恢复未验证。
- b 停止期间占用自动 SSH 端口，start 自动改分配并更新 alias，实际 SSH 和 a→b SSH 成功。
- 原生 SIGINT 在 c 的 QEMU 身份提交后注入，CLI 退出 130；再次 up 保留 c 的 PID、UUID
  和根盘 inode，随后管理 SSH 成功。已创建节点的数据不是取消时的清理对象。
- 使用真实 Ansible 对同一 Pigsty 兼容 inventory 执行 `ansible.builtin.ping`，两个节点均
  SUCCESS/pong。通过隔离的 Farrow SSH fragment 连接；这是实际连接验证，不是完整部署 Pigsty。
- 受控移走 a 中的 Guest key：修复前无警告，修复后管理 SSH 可用、退出 0 且携带
  control-ssh 限制。随后恢复原文件、up 清除限制、a→b SSH 成功。没有将私钥塞进 argv/日志。

### 按用户路径的覆盖与缺口

| 路径 | 当前证据 | 未被这份证据覆盖的范围 |
|---|---|---|
| 安装/PATH/CLI-helper | installer trust/retained release/pair 回归；本机 doctor/setup dry-run；构建 paired helper | 干净机器真实安装，两个真实 PATH 版本切换、交互管理员认证 |
| 无配置/显式配置/已有状态 | CLI 回归；原生 -f、当前目录发现、既有 0.7 状态 | 无依赖干净宿主的一整次 interactive up→setup→retry；脚本 next 指引 U12 |
| 生命周期/部分节点/取消 | 原生 up/start/stop/reload、partial、no-wait、SIGINT、端口恢复；restart/recreate 和停止前保护回归 | 专用宿主 reboot、真实网络服务退出/残留 socket 修复 |
| 网络/DNS | 原生健康 vmnet；真实冲突/未知归属/permission 根因合并回归；Guest readiness 不访问公网的源码与 shell 测试 | 故障 DNS / split DNS / VPN 原生注入 U07；未修改当前宿主 DNS |
| 镜像/缓存/代理 | 实际 618 MB 下载；恢复、引用保护、断点/取消、私有源保留、摘要不符测试；无 QEMU 的 info 回归 | 实际代理掉线、私有镜像服务故障；完整原生离线镜像制作 |
| 共享/Guest/数据盘 | 节点隔离原生；当前 key 限制原生；数据盘 reset 边界 shell 回归覆盖缺设备/probe/I/O/busy 等不格式化 | 完整 macOS 共享 U10；嵌套项目 UID/GID 与多 share 降级 U08；不对用户磁盘注入损坏 |
| 诊断/清理 | 首次 setup 无 state 的 logs 回归；文本/JSON/退出码检查；原生清理记录 | 宿主网络 uninstall/purge 的真实管理员步骤；本机共享服务保持运行 |
| 升级/性能 | 0.7 VM 接续源码、镜像与磁盘身份、实际时长样本 | 0.8 已发布资产安装不存在；原生 Linux amd64 与宿主 reboot 缺专用环境 |

### 0.8 建议范围与后续设计

首批只纳入 U01/U02/U03/U04/U06/U11/U13/U15、U10 的准确诊断和已存在的五项恢复修复；
2026-09-21 新增 U16 的首次安装认证顺序修复。
共享仍按节点隔离；完整 UID 映射、多挂载部分省略、新网络模式不进入本轮。
必须明确 macOS 目录共享的当前限制，不能把 U10 的诊断当作支持已经修好。

- **U10 推荐方案：** 先获得 QEMU 可接受的目录 FD 接口并补原生身份/rename/symlink
  并发测试，再宣称 macOS 共享可用；若 0.8 不承担自定义 QEMU 分发，先清楚限制共享的平台
  范围。当前 QEMU 的 9p local_init 直接使用 open(O_DIRECTORY)，可查
  [QEMU 源码](https://raw.githubusercontent.com/qemu/qemu/v11.1.0/hw/9pfs/9p-local.c)。
  Darwin 的卷/inode 路径会先转为普通路径再查找，不能视为 FD 的安全替代，可查
  [XNU 路径查找源码](https://raw.githubusercontent.com/apple-oss-distributions/xnu/main/bsd/vfs/vfs_lookup.c)。
- **U05 最小可交付：** 已有管理 SSH 认证与 UUID 绑定后，从原部署 key 派生公钥比对；
  使用不记录 stdin 的执行接口，Guest 在锁内用原子写入修复缺失 key，复验权限和节点间
  SSH。存在不同 key、软链或身份不明时停止，不覆盖。此传输/安全边界未在本轮实现。
- **U07 最小可交付：** 先复现管理网 DNS 首选失败，再提供明确的 DNS 来源/测试结果；
  显式配置与回退要保留 resolver 的接口/域名作用域，不自动改为公网 DNS。
- **U08 最小可交付：** 若允许跳过一个挂载，需使运行 invocation、继承 FD、已应用规格和
  Guest warning 表达相同事实，并说明恢复是否需要冷启动；不能只吞 Open 错误。
- **U12/U14：** 先完成配置来源契约和独立前置阶段测试，再扩大自动续跑范围。

发布前必须通过最终源码 `make check`、双语文档检查，以及约定平台的真实安装→首次 SSH。
macOS 共享的支持声明必须与 U10 一致；原生 Linux、专用机 reboot、管理员安装/卸载仍是
缺失环境的验收项。mini pc 链接尚未收到，没有猜测或宣称已完成对标。

源码检查、构建、真实 VM 验证与安装/发布分开：本轮交付工作区；未 commit、push、tag、
替换 Homebrew 可执行文件或发布网站。性能和最终清理结果如下。


### 最终计时、清理与验证结论

时长均为命令墙钟时间，不混用下载、启动和热路径。样本来自 macOS arm64/HVF、
1 CPU / 1 GiB 的 u24@20260801.0.0 测试 VM，不能外推为所有平台 SLA。

| 场景 | 结果 | 样本/限制 |
|---|---|---|
| 镜像下载 | 146.478 s；618,417,664 bytes | 真实 Homebrew 0.7.0 下载，非源码新安装速度；已验证缓存供后续使用 |
| 0.7 首次创建到 ready | 20.121 s；随后 exec id 成功 | a，已有健康宿主 QEMU/网络，不含安装依赖 |
| 已有磁盘启动 | 13.735 s | 最终 start a；/data 中测试内容保留，UUID/根盘 inode 不变 |
| 最终健康重复 up | 20 次；p50 **0.498 s**，nearest-rank p95 **0.560 s**；0.468–0.696 s | 两台节点，包含新增控制 key 检查；PID/UUID/根盘 inode 不变、无 Guest 警告 |
| 检查新增前的 warm 对照 | p50 0.435 s，p95 0.454 s | 同环境较早的 20 次观测，非受控 benchmark；不把增加必要检查说成性能优化 |
| snapd.seeded | 本轮重启样本 573 ms | `systemd-analyze blame`；不等价于首次 seed 的耗时，不据此改镜像 |

普通 destroy 已实测保留三块持久盘，包括 0.7 存在节点目录中的盘迁移到保留盘目录，
inode 均未变化；随后的无部署状态 purge 删除三块保留盘、keys、state、events。
又创建单独的 d 测试节点，`destroy --force --delete-persistent` 明确删除 1 块持久盘且
不再声称该盘被保留。最终 purge 保留了 618 MB 镜像缓存和共享宿主网络，符合其职责。
上述事实存于 `native-cleanup.json`。随后移除本轮独有的缓存和隔离 SSH home，测试目录
已完全清理，没有遗留测试 VM；实际日志和复现脚本保存在外层证据目录。

最终检查：

- 源码 `make check` **通过**：unit/race、vet、staticcheck、deadcode、errcheck、govulncheck、
  四目标交叉编译、installer/image 边界和许可证检查。原生离线镜像修改测试按现有规则
  SKIP（未提供其专用 source 参数），不计为真实 VM 镜像制作验证。
- 文档仓库 `make check` **通过**：Hugo 警告作为错误，215 个 HTML 页面内链/资产检查通过。
- 已构建当前源码与配对 helper；最终可执行文件路径由 `build.log` 和每次 native 记录给出。
- 两个仓库 `git diff --check` 通过。初始未跟踪文件摘要不变，已有改动保留；两仓库 HEAD
  均未改变。本轮没有提交、推送、打 tag、网站部署或替换 Homebrew 安装。
- 本机原四台 pg-* VM 的 PID 78905–78908、vmnet PID 866 保持；安装的 CLI 仍是 0.7.0
  (`9c6d489`)。不能用本轮工作区测试推断 Homebrew 用户已获得这些修复。

完整清单共 19 项，其中 9 组局部修复/诊断改进已交付；已存在的五项成果只记验证。
未完成项有明确证据级别、最小范围和依赖，尤其是 macOS 完整共享、旧 Guest 私钥原位
注入、DNS 回退、脚本 applied-state setup 指引和全局镜像前置阶段的节点隔离。
