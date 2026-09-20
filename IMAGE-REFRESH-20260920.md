# Farrow 镜像更新记录：2026-09-20

已完成本机缓存、本地静态仓库和 m0 局域网仓库更新。Catalog `2026092001` 共 37 个工件：新增 10 个，全部 27 个旧工件及其元数据原样保留。默认 Family 仍为 `u24`。

| Family | stable | 上游小版本 | 架构 |
|---|---|---|---|
| d12 | 20260909.2596.1 | Debian 12.15 | amd64 / arm64 |
| d13 | 20260914.2601.1 | Debian 13.7 | amd64 / arm64 |
| u22 | 20260913.0.0 | Ubuntu 22.04.5 | amd64 / arm64 |
| u24 | 20260911.0.0 | Ubuntu 24.04.5 | amd64 / arm64 |
| u26 | 20260918.0.0 | Ubuntu 26.04.1 | amd64 / arm64 |

## 历史调整与本次接续

查阅了 2026-09-03 的实际任务记录（`01a06653-2de1-7bc3-9f3b-1a8ce1d09ad3`）、现有 Catalog、流水线源码，以及 m0 保留的 `/var/tmp/farrow-debian-locale-build-20260903/src/packaging/image-pipeline`。历史源码副本保存在本次构建目录的 `history/`。

- Debian 12/13：离线安装带摘要锁定的 `xfsprogs` 及依赖，生成 `en_US.UTF-8`，默认保持 `C.UTF-8`；保留已有 dba UID 88 / admin GID 88、密码锁定、SSH 和机器身份清理策略。本次 Debian 13 的 XFS 包更新到 `6.13.0-2+deb13u1`，Debian 12 保持 `6.1.0-1`。
- 已确认缺陷：历史发布镜像有 locale 调整，但主分支构建配方没有包含，刷新基础镜像会丢失该能力。本次恢复原历史实现，并在脚本、宿主端 marker 校验及官方 bundle 验证中加入条件。
- Ubuntu：查到的已发布工件与官方摘要对应，继续保留 Canonical 原始镜像；dba/admin、SSH、主机名和网络属于 Farrow cloud-init 的部署配置。没有发现已发布 Ubuntu 工件包含 `snapd` 离线裁剪的证据。本次也未加入这类修改。
- Ubuntu 选用固定日期的 release cloud image，不使用会移动的 `current`/`release` 链接，不替换用户锁定的旧版本。Rocky / CentOS 工件不变。

| 优先级 | 场景与证据 | 根因与归属 | 最小改动与新行为 | 验收、规模、依赖、状态 |
|---|---|---|---|---|
| P1 | 刷新 Debian 后丢失 en_US.UTF-8；代码已确认，历史发布工件与 m0 历史源码交叉验证 | Farrow 自有派生镜像；已发布调整未写回配方 | 恢复既有 locale 生成，默认 C.UTF-8；缺失或错误的 locale marker 阻止构建 | 小范围；依赖 libguestfs、固定镜像与签名 apt 元数据；回归、4 个 Debian 原生 VM、重复构建均完成 |
| P1 | 本机/LAN stable 仍指向 8 月镜像；当前已复现 | Farrow 自有仓库及可追加缓存，旧 VM 引用不可覆盖 | 增加 10 个版本化文件、37 工件签名 Catalog，保留全部旧条目 | 小范围数据与 fixture 更新；依赖上游摘要及原生 HVF/KVM；已完成 |

## 真实执行证据

- 10 份上游输入通过官方 SHA-512（Debian）或 SHA-256（Ubuntu）校验；Debian 包来自验证通过的 Debian 签名 apt 索引，离线修改时无网络。每个最终镜像通过 qcow2 单层、大小、完整性和最终 SHA-256 校验。
- 4 个 Debian 工件均构建两次，源文件、包锁、工具版本、归一化 marker、locale 和 qcow2 结构结果一致；最终 qcow2 字节不完全相同，不声称位级可复现。amd64 额外全文件对比的文本内容差异为 `/run/blkid/blkid.tab` 和 `/var/log/dpkg.log` 的运行/时间记录，并有文件系统元数据差异。arm64 的额外 TCG 全文件扫描耗时较长而停止，未将其作为通过项；必需的重复构建契约对比和原生启动检查已通过。
- 每个新工件在对应原生架构上以隔离 overlay 启动：macOS arm64/HVF、m0 Linux amd64/KVM。检查 UEFI、双网卡、SSH、dba UID/GID 88、Python；Debian 额外验证 C.UTF-8 默认、en_US.UTF-8 和 XFS 创建/挂载/读写。
- 真实 Farrow 路径：已安装的 0.7.0 读取新 Catalog 并创建 U24、D13 两个隔离节点；当前源码接续，Ansible ping 两台通过，XFS 数据盘读写通过。重复 up 0.490 秒，stop 后 start 13.645 秒。节点、私钥、数据盘和临时测试缓存已清理。
- 初轮自建测试脚本遇到三个问题：未复用完整 admin 组修正规则、Python 命令引号错误、隔离 HOME 太长导致 Ansible ControlPath 超限。修正测试脚本后重跑通过；未修改 Ubuntu 原始字节来绕过这些测试问题。
- 新 Ubuntu 的 snapd.seeded 单次测量为 2.752–14.738 秒（不同宿主/架构），没有重现历史 44 秒；没有据此作系统服务优化或声称跨版本性能提升。

| 工件 | SSH 秒数 | 校验后大小（字节） | SHA-256 |
|---|---:|---:|---|
| d12-20260909.2596.1-amd64 | 17.438 | 767492096 | `ad255513c30684f7bc833ba8aaa55745764d957c2ea587ef28adf78548dfcfbf` |
| d12-20260909.2596.1-arm64 | 13.254 | 750583808 | `b4095d161fd3b551df4cc47db9019cb98b96c45ad3320a00c6ef9ca3425ca676` |
| d13-20260914.2601.1-amd64 | 14.377 | 532873216 | `3ec9fc9ca0adca84dbdcf9297687e1f0b893138ed4205d6584b47ecda4f76464` |
| d13-20260914.2601.1-arm64 | 10.222 | 582090752 | `9fe2f0a4fe6a43ff04f741d41962f1107b840c14b80f23b7c967a731e0fe1e6e` |
| u22-20260913.0.0-amd64 | 22.319 | 735388672 | `9144540e8af7637d258b50dbabe82ce1aa6752c9574fedfb048270da0e087899` |
| u22-20260913.0.0-arm64 | 17.434 | 704972800 | `ab5fcc80611a98bf999018045119d87b3a0e7c78f3b43b254b93d5c22bae3ff6` |
| u24-20260911.0.0-amd64 | 20.110 | 625256960 | `612b2c0cc1bc413a6cb8c38fd611794caf0f2b436c50013d8b3794db12ad7354` |
| u24-20260911.0.0-arm64 | 12.706 | 619621888 | `7b682958a67ff5de068e36de6af8b75fa645d296af5a70d6500527f6a33781db` |
| u26-20260918.0.0-amd64 | 22.453 | 864411136 | `4908fb59ccd4e87ae4e8e973b7ef56f535448eacb24a87fd787270c0048987bc` |
| u26-20260918.0.0-arm64 | 13.776 | 944823296 | `8dc812bc6356d0abf825d8029f25f1b71f02cb103e1d0cc5c17fbb2572322972` |

## 交付位置与使用

- 本机缓存：`/Users/vonng/.farrow/images`，新增 10 个只读文件；通过已安装的 Farrow 0.7 从 `http://m0/farrow` 实际拉取并逐一验证。
- 本地静态仓库：`/Users/vonng/pgsty/repo/farrow`。
- m0 静态仓库：`/www/farrow` → `/data/nginx/farrow`；HTTP 入口：`http://m0/farrow`。
- 人工维护源：`packaging/image-repository/repo.yaml`；内嵌副本：`internal/image/default-catalog.json`。
- 本地、m0 HTTP 和源码 Catalog 字节一致，SHA-256 为 `23e8dbf6c19bd192d56c6d71eb30901f17945b3487e427a43abe108463780306`。m0 使用已有生产密钥签名，私钥未离开 m0；Farrow 0.7 正常验证签名。
- 本机默认 Catalog、本地仓库槽、m0 仓库槽均已激活新 revision。默认 `image info u24` 显示 `20260911.0.0` 且 cached=true。后续从局域网获取新镜像可使用：

```bash
farrow update --repo http://m0/farrow
farrow up --repo http://m0/farrow
```

本次没有同步公开的 repo.pigsty.io / repo.pigsty.cc；在公开仓库尚未更新时，向其请求 Catalog 更新会遇到较旧 revision，防回滚检查应继续拒绝降级。已缓存的新镜像可以正常使用。

## 保留与回滚边界

- 现有 4 台 pg-meta/pg-test VM 的状态 JSON 前后一致：PID 78905–78908、端口、资源和旧镜像引用均未改变。旧 U24 缓存 SHA-256、大小、inode 前后一致。没有执行这些节点的 up、restart、recreate 或 destroy。
- 仓库采用同文件系统目录交换切换；旧仓库备份在本地 `data/backups/farrow-2026090501-before-20260920`，m0 `/data/nginx/.farrow-backup-2026090501-20260920`。镜像工件使用硬链接保留，无需重复复制旧 20GB 数据。
- 缓存原 manifest registry 备份位于构建证据 `evidence/manifest-registry-before`。若确需回退已激活 Catalog，必须使用现有 `image sync --allow-downgrade` 并明确选择对应仓库槽；不要手工删除 high-water 状态。
- 本次创建的 3 个构建/测试容器均清理；既有容器与镜像不动。源文件、包锁、构建产物及证据仍保留。

## 验证与发布状态

- `make check`：通过（包含测试/race、静态检查、跨平台构建、镜像流水线、安装器和许可证检查）。
- `make build`：通过，工作区 `bin/farrow` 为当前未提交源码构建。
- 文档 `hugo` 构建与两个仓库 `git diff --check`：通过。
- 源码、CHANGELOG、CONTRIBUTING、中英文镜像与流水线文档已更新，已有 UX 修改保留。
- 已完成：本地构建、10 个原生 VM 验证、Farrow 0.7 兼容与源码接续、Ansible 连接、签名、LAN HTTP 消费和本机缓存激活。
- 未执行：Git 提交/推送、tag、应用发布、公网仓库发布、Homebrew CLI/helper 替换、完整 Pigsty 安装、宿主重启。已安装 CLI 仍为 0.7.0。

全部详细证据位于 `/Users/vonng/pgsty/repo/data/build/farrow-20260920`：
`source-lock.json`、`source-receipts.json`、`bundles/*/validation.json`、`evidence/official-bundle-verification.json`、`evidence/repeat-build-contracts.json`、`evidence/native-smoke-matrix.json`、`farrow-integration-final/runs.jsonl`、`evidence/cache-update.log`、`evidence/catalog-byte-identity.json`、`evidence/existing-deployment-{before,after}.json`、`evidence/make-check.log`、`evidence/hugo-build.log`。
