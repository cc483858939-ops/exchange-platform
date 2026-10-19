# 半自动生产 CD

工作流：`.github/workflows/deploy-production.yml`，名称 **Production Deploy**。
只支持 `workflow_dispatch`，默认 `check`，仅 `main`；生产业务目录固定为
`/srv/exchange-platform`，Compose 固定为 `deploy/compose.prod.yml`。
**首次接入只运行 check；本次搭建不授权生产升级或 Schema 20 迁移。**

## 实现与信任边界

- GitHub-hosted Ubuntu runner；`contents: read`、`actions: read`，无仓库写权限。
- 两次调用 `scripts/cd/verify-ci.py`：审批前、`production` Environment 审批后。
  SHA 必须为手动填写的完整 40 位小写 SHA，并等于 dispatch 的 `github.sha` 与实时 main。
  查询 **ci.yml** 对应 workflow ID、该 SHA/main 的最新 push/manual run（不接受 PR merge SHA），
  只接受 completed/success，并逐个验证后端单测、PostgreSQL/Redis 集成、Kafka 初始化、
  容器构建 job 及关键 step 成功；缺失、跳过、失败、排队、运行中都停止。
  不触发或替代现有 CI，不因旧 SHA 曾经通过而放行。
- concurrency 固定生产组，`cancel-in-progress: false`。GitHub 并发组不保证排队顺序；
  每次仍重新验证 SHA。服务端另有独占锁，失败/未知部署有持久化阻断标记。
- SSH key 只能发送 `check <sha> <run-id>-<attempt>`、`deploy ...`、`status ...`。
  服务器不接受 shell、路径、环境变量、ACK、数据维护命令或上传的部署脚本。
- 独立账号不加入 docker 组；它只能 sudo 到 root 管理的固定入口。
  **Docker/root 部署权限实际上等同于很高的宿主机权限**。forced-command 限制调用范围，
  不是容器安全隔离；已批准的 main 代码和 Dockerfile 也属于高信任内容。
  必须保护 main、CI/CD 文件和生产审批，严格限制能修改工作流、读取密钥或批准部署的人。
- root 入口从干净环境执行固定 Git/Docker/官方部署脚本，不继承 SSH 客户端环境。
  安装文件与仓库应由 root 管理，部署账号不能写入。Git 远端认证使用 VPS 已有的只读拉取方式，
  不把 Actions SSH 私钥用于 Git 拉取，也不启用 agent forwarding。

## 1. 网络与 SSH：先确认，再安装

本次本机检查发现 `myserver` 配置为 `154.219.115.17:22`、用户 root、专用本机 IdentityFile。
TCP 建立后在密钥交换前断开，报 `kex_exchange_identification: Connection closed by remote host`。
这不证明 GitHub-hosted runner 可达，也不能确认原因是防火墙、sshd、fail2ban 或上游限制。
未登录 VPS，未核实现场版本、指纹、sshd 策略或防火墙，未安装账号和入口。

管理员应从云厂商控制台或既有可信管理通道检查 sshd/接入策略和连接日志，确认原因。
**不要为接入 Actions 广泛开放 SSH、关闭主机校验或修改防火墙放行未知来源。**
如果普通 hosted runner 无法安全到达，停止接入；可单独审核以下方案：

1. GitHub 较大 hosted runner 的固定出口 IP，再由管理员按明确来源制定接入策略；或
2. 独立、临时的部署 runner/可信管理网关，通过已有 VPN/私网到 VPS；不要在生产 VPS 上
   注册可运行任意 PR 的通用 self-hosted runner。

这些方案需要单独网络审核及 workflow 改动，本 PR 不实施。

## 2. 管理员一次性安装受限入口

在网络和主机身份确认、PR 审查合并后，由管理员在**独立 bootstrap 目录**获取已审核提交的
`scripts/cd/server.py` 与 `scripts/cd/gateway.sh`。不要为了安装 CD fast-forward
`/srv/exchange-platform`，也不要运行部署、迁移、Compose up 或 ACK。

依赖：Linux、Python 3、sudo、flock、Git、Docker Compose v2；既有生产 .env 和部署 state
必须有效。使用 root 管理的仓库、Docker 配置与 Git 拉取凭据。下面是管理员操作模板，
`/root/reviewed-cd` 表示已经核对提交和文件内容的独立源码副本：

```sh
useradd --create-home --shell /bin/sh exchange-deploy
passwd -l exchange-deploy
chown root:root /home/exchange-deploy
chmod 0755 /home/exchange-deploy
install -d -o root -g root -m 0700 /home/exchange-deploy/.ssh
install -d -o root -g root -m 0755 /usr/local/lib/exchange-platform-cd
install -o root -g root -m 0644 /root/reviewed-cd/scripts/cd/server.py /usr/local/lib/exchange-platform-cd/server.py
install -o root -g root -m 0755 /root/reviewed-cd/scripts/cd/gateway.sh /usr/local/sbin/exchange-production-cd
install -d -o root -g root -m 0700 /srv/exchange-platform/backups/deploy
```

若账号已存在，先审核其现有用途，不重复 useradd 或覆盖 SSH 授权。
使用 `visudo -f /etc/sudoers.d/exchange-production-cd` 写入并验证：

```sudoers
exchange-deploy ALL=(root) NOPASSWD: /usr/local/sbin/exchange-production-cd *
```

该 wildcard 只允许 wrapper 接收参数；wrapper 强制**一个参数**，Python 再做完整匹配。
不得授予 sudo bash/python/env/docker、sudo ALL 或 Docker socket 直接访问权限。
文件 root:root 0440，运行 `visudo -cf /etc/sudoers.d/exchange-production-cd`。

在可信本机或独立管理员环境运行 `ssh-keygen -t ed25519 -f exchange-production-actions`，
生成**新**的部署身份。私钥只放入 production Environment Secret，不发到聊天、日志或仓库。
不要复用 root 登录私钥。将公钥加入 root 管理的 `authorized_keys`，一行格式如下
（替换公钥占位内容，保留 forced command 和 restrict）：

```text
restrict,command="/usr/bin/sudo -n /usr/local/sbin/exchange-production-cd \"$SSH_ORIGINAL_COMMAND\"" ssh-ed25519 PUBLIC_KEY_FROM_NEW_DEPLOY_IDENTITY exchange-production-actions
```

`authorized_keys` 为 root:root 0600。确认 sshd 的现有用户白名单、公钥登录策略允许该身份，
且身份不具备密码登录能力；需要调整接入规则时由管理员单独审核。不要关闭现有管理会话。
旧 root 私钥不复制到 GitHub。

## 3. 确认主机身份

从可信 VPS 控制台读取：

```sh
ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub
cat /etc/ssh/ssh_host_ed25519_key.pub
```

通过独立可信渠道核对 SHA256 指纹，再构造 `known_hosts`：22 端口为
`HOST ssh-ed25519 PUBLIC_HOST_KEY`；非 22 端口为
`[HOST]:PORT ssh-ed25519 PUBLIC_HOST_KEY`。
HOST 必须与 `DEPLOY_HOST` 完全一致。本次连接未取得已验证的指纹，不能把本机旧记录
或未验证的 `ssh-keyscan` 输出直接当作信任根。
工作流使用 `StrictHostKeyChecking=yes`，不调用 `ssh-keyscan`。

## 4. GitHub production Environment

仓库 Settings → Environments → New environment，准确命名 **production**。
先配置 protection rules，再放入密钥；环境名写错可能创建一个没有预期审批规则的新环境。

| 类型 | 名称 | 值 |
|---|---|---|
| Secret | `DEPLOY_SSH_PRIVATE_KEY` | 新身份完整私钥，仅粘贴在 GitHub Secret 页面 |
| Secret | `DEPLOY_SSH_KNOWN_HOSTS` | 上述独立确认的主机公钥记录 |
| Variable | `DEPLOY_HOST` | 已确认的 VPS SSH 地址，不带协议 |
| Variable | `DEPLOY_PORT` | 实际 SSH 端口，例如 `22` |
| Variable | `DEPLOY_USER` | `exchange-deploy` |

配置 Required reviewers、Prevent self-review；禁止管理员绕过（如计划支持），
Deployment branches and tags 只允许 **main branch**，不允许同名 tag。
环境保护规则在 job 启动及访问 secrets 前生效。
参见 [GitHub 部署保护规则](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/control-deployments)。
若仓库计划不支持所需审批规则，停止启用生产 deploy，先解决审批机制。
建议保护 main、禁止 force push，要求 CI 和审查；为 `.github/workflows/`、`scripts/cd/`、
`scripts/deploy-production.sh` 配置现有 CODEOWNERS/分支保护，限制 Actions 修改权限。
本 PR 不擅自修改组织、仓库或人员审批策略。

## 5. 首次只跑 check

合并并配置环境后，等待 **main 当前完整 SHA** 的现有 CI 全部成功。
Actions → **Production Deploy** → **Run workflow**：Branch 选 main，mode 选 **check**，
target_sha 粘贴该完整 SHA；审核后放行 environment job。

check 只执行 SSH 验证、Git fetch/版本检查、官方
`bash scripts/deploy-production.sh CHECK`、只读数据库事务、服务健康和公网 GET。
它不会 Git merge、修改 deployment state、迁移、构建或替换容器；仅写 0600 审计报告。
报告包含当前/目标 SHA、runtime/Compose state、当前和预期 Schema、迁移风险、数据数量、
六项服务健康、CDC connector/tasks、两个公网探针及 Public Recommendations 结果。
公网 Recommendations 返回非空 `items` 且具有 post 对象才通过，不记录帖子或 tracking token。

Compose `pending` 会显示经过凭据字段脱敏的 diff，并**失败停止**；不会 ACK、应用 Compose
或升级 Schema。由管理员独立审核、正确应用、验证，再运行官方
`bash scripts/deploy-production.sh ACK-COMPOSE-APPLIED <full-sha>`，然后重新运行 check。
不要仅为了让 CD 通过而 ACK；本次任务未授权这一步。

## 6. 后续人工触发 deploy

以后真正决定升级时，再在网页选择 mode **deploy**、填写 main 的完整 SHA 并审批。
等待审批期间 main 前进会停止，必须用新 SHA 重跑 check，不能继续旧审批静默部署新 HEAD。
服务端反复确认 origin/main、服务器 main、干净工作树、FF ancestry、deployment ancestry；
Compose 状态合格后才 FF 到固定 SHA，并执行原有：

```sh
bash scripts/deploy-production.sh DEPLOY-PRODUCTION
```

官方脚本仅增加可选 `DEPLOY_EXPECTED_SHA` 门禁与不可变 SHA 的 FF，保留原有备份、迁移、
Kafka/CDC 初始化、运行时更新与 ACK 逻辑。入口不会复制这些逻辑。
FF 使用 `--no-overwrite-ignore`，遇到 ignored 文件覆盖风险也停止。
没有 Compose 差异时允许保留较早的 Compose state SHA，只要 ancestry 和路径 diff 均正确。
最终比对 Git/runtime SHA、目标 Schema、服务/CDC/公网健康及前后镜像账号、镜像帖子、
总帖子、总媒体和镜像媒体数量；**任何数量下降**均停止并交人工调查，不自动回滚。
正常用户删除也可能触发此保守门禁，需要人工核实原因。

不会检查 Pages，不运行 RSSHub、DevData、reset、embedding backfill、数据清理、volume 删除、
基础设施重建或旧数据库/容器恢复。新入口版本需要管理员单独审核后重新 install，
仓库更新不会自动覆盖 root 管理的入口。

## 7. 日志、断线、失败处置

VPS：`backups/deploy/<run-id>-<attempt>.json`，目录 0700、文件 0600，包含开始/结束时间、
完整目标 SHA、官方脚本退出码（如已执行）、最终退出码及脱敏阶段记录。
仅保存官方脚本白名单结构字段，原始 build/SQL/异常/env 输出不落盘、不回传 Actions。
失败排查需管理员读取容器与系统日志，避免把可能含凭据的原始日志上传到 Actions。
Actions job summary 和 30 天 artifact 包含 `result.json`、`execution.jsonl`。

deploy 只提交一次。服务器 detached worker 持有独占锁，SSH 断线不杀死部署。
runner 轮询 status、检查最终退出结果；45 分钟等待超时或 SSH 失败均标记 **unknown**，
额外只读 status 一次，**绝不自动重新发送 deploy**。
服务器重启/进程被杀时 running 记录可能不再更新，不能当作成功或据此重跑。

管理员先从可信通道检查：

```sh
cat /srv/exchange-platform/backups/deploy/active.json
cat /srv/exchange-platform/backups/deploy/RUN_ID-ATTEMPT.json
lslocks | grep exchange-platform
pgrep -af '/usr/local/lib/exchange-platform-cd/server.py|scripts/deploy-production.sh'
```

随后核对实际 HEAD、runtime/Compose state、Schema、服务健康及备份；不要直接重复部署。
纯预检失败且尚未开始 FF 时可释放阻断标记；一旦开始 FF 后失败/unknown，标记保留。
确认所有相关进程/子进程结束、官方部署锁释放、实际状态已核清，并由负责人决定下一步后，
才由管理员把 `active.json` **改名保存为审计记录**再重新发起人工 check。
不要在 worker 尚存活时解除标记；不要盲目重试迁移或自动恢复旧 Schema。

## 本次验证边界

实际 SSH：认证前连接被远端关闭，VPS 预检 **BLOCKED**；未验证 GitHub-hosted 来源可达性。
本机完成固定 SHA、CI job/step、Compose gate、日志脱敏、退出码、数量下降与断线不重发的
模拟测试和 Actions 静态检查。生产身份、主机指纹、Environment/Secrets、审批规则及真实
check 仍需以上设置完成后验证；当前生产版本/Schema 没有在此次连接中重新确认。

2026-10-19 本地验证：Ubuntu Linux 18 项离线测试全部通过（包含独立进程的 detached worker、
实际文件锁、防重放和 0600 权限）；官方部署状态机 13 项通过；actionlint v1.7.7、
Python/Bash 语法、git diff --check 通过。Windows 离线测试 17 项通过、1 项 Linux/root
生命周期测试跳过；Linux 已补齐该项。既有 CI 的容器 job 只增加这些离线回归和入口语法检查，
没有删除或放宽原检查。新增 sudo 仅在临时 hosted CI runner 内用于根身份入口模拟，
模拟副本使用临时路径和无副作用部署函数，不连接生产服务。
