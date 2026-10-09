# DevData mirror refresh

DevData 的完整快照由 `cmd/devdata` 负责生成和同步。增量刷新是一个可选的
operator/scheduler 命令，不由应用进程自动启动。

默认 registry 路径按运行目录解析：生产镜像从 `/app/config/sources/x_sources.json`
读取运行时 registry；`devdata/testdata/x_sources_v1.json` 继续作为测试 fixture，
不会被生产容器默认使用。生产镜像中的命令已经编译为
`/app/go-exchange-devdata`，其 `.devdata` 目录必须挂载持久化 volume。

## 首次初始化和完整校准

先启动 PostgreSQL、Redis、MinIO（如需媒体镜像）以及 RSSHub，再从
`Go.exchange` 目录执行完整刷新：

```powershell
go run ./cmd/devdata refresh --source=rsshub --allow-destructive --reset-checkpoint
```

完整刷新会重建 `.devdata/x_latest.json` 的完整账号/帖子清单，并保留 RSSHub
的可恢复 checkpoint 语义。`rebuild` 只读取已有的完整快照，不会重新抓取源数据。

## 24 小时增量轮询

外部 scheduler 每 6 小时执行一次，4 个分片组成约 24 小时的完整周期：

```text
0 */6 * * * cd /path/to/repository/Go.exchange && go run ./cmd/devdata refresh-incremental --source=rsshub --shard=auto
```

启用账号按稳定的 registry key 分成 4 个固定分片。`--shard=auto` 使用 UTC
6 小时时间桶选择分片，因此每个账号约每 24 小时刷新一次。推荐 scheduler
使用 UTC，在 00:00、06:00、12:00、18:00 执行。也可以手动运行
`--shard=0` 到 `--shard=3`；同一时间桶重复运行同一分片是幂等的，但仍会
重新请求源数据。旧的每小时 scheduler 必须改为每 6 小时运行，否则同一
分片会在 6 小时时间桶内被重复刷新。应用进程不会自动安装或启动 scheduler。

默认每个账号先请求最近 20 条源帖子；窗口恰好填满且和完整基线没有重叠时，
才回退请求 60 条。`DEVDATA_INCREMENTAL_FETCH_COUNT` 或 `--fetch-count` 可
将初始窗口设置为 5 到 60 之间的值。遇到 RSSHub 429 时本轮立即停止，不会额外
重试，也不会写入部分增量快照。

增量检查点独立保存为 `.devdata/x_incremental_checkpoint.json`，不复用全量
抓取的 `x_fetch_checkpoint.json`。每个账号成功抓取后立即原子保存其数据和
完成状态。中途失败、取消或遇到 429 时保留检查点；下次只抓未完成账号。
如果 429 提供 `Retry-After`，检查点会记录冷却期限，期限内重跑不会请求源。
`--checkpoint` 可指定增量检查点路径，但不能指向正式快照或同目录的全量
检查点。

恢复总是先完成检查点记录的原分片。`--shard=auto` 恢复成功后继续处理本次
运行开始时到期的分片；即使间隔 24 小时后分片编号再次相同，也会执行新的
时间桶。手动指定分片时必须与待恢复分片一致，且只恢复该分片。

检查点包含 `fetching`、`ready`、`synced` 三个阶段：整组抓齐后保存待提交
快照，再同步数据库，最后写正式快照并移除检查点。数据库已提交且 `synced`
阶段已保存时，下次只完成快照写入；如果在提交后、阶段保存前崩溃，会通过
现有幂等同步重放已保存的整组数据，不会重新抓取或重复插入同一来源帖子。
正式快照已写入、检查点尚未删除时，下次确认其指纹后完成清理。

恢复会校验来源、registry/eligibility 配置、抓取窗口和基线快照指纹。
配置变化、基线被其他操作改写或检查点损坏时明确报错并保留现有文件，
不会自动丢弃进度或覆盖新基线。检查点累计请求数以 `requests_total` 输出。

增量刷新要求 `.devdata/x_latest.json` 是与当前 registry 和 eligibility policy
匹配的完整有效基线。缺失、损坏或过期时，应先运行一次完整 `refresh`；增量命令
不会自动删除或重置快照，也不接受 `--allow-destructive` 或
`--reset-checkpoint`。

## 暂停、恢复和定期校准

暂停 scheduler 不会改变快照。短暂停顿通常可以直接恢复增量任务；长时间或
不确定时长的暂停，建议完成待恢复的增量检查点，再运行一次完整 `refresh`，
然后恢复每 6 小时的四分片任务。
源可见性受每个账号最多 60 条的窗口限制，不能承诺增量任务无限补齐暂停期间的
历史帖子。增量刷新只会新增或更新选中账号的帖子、资料和未解析的媒体，不会
根据短源窗口推断删除，也不会退休未出现在本轮窗口中的历史帖子。

## Production scheduling

推荐把两个命令看成不同的运维职责：

- `refresh-incremental` 负责 freshness。每 6 小时运行一次、每次一个分片，
  因此每个账号约每 24 小时刷新一次；恢复旧检查点时可以先补跑原分片。
- `refresh` 负责 reconciliation / cleanup、修复 source/account drift，
  并更新完整的恢复基线。建议每天运行一次：

```powershell
go run ./cmd/devdata refresh --source=rsshub --allow-destructive
```

普通的每日 full refresh 不要求 `--reset-checkpoint`；只有 operator 明确
要丢弃已有的可恢复 checkpoint 时才使用它。增量刷新是 non-destructive：
某个帖子没有出现在本次有限 source window 中，不会让它退休。长期只运行
增量任务会使 DB mirror history 持续累积，所以 full refresh 是推荐的定期
对账，而不是每次增量刷新正确性的前置条件。

`refresh`、`rebuild` 和 `refresh-incremental` 共用 PostgreSQL mutation
advisory lock。full refresh 或 rebuild 正在运行时，增量任务可以安全跳过
本次 invocation；下一个 6 小时时间点的 scheduler 会继续。完整校准应安排在
增量检查点完成之后；如果 full refresh 或其他操作改写了待恢复检查点的基线，
增量任务会报告冲突，需要 operator 对账后处理，不能保证自动补齐全部历史。

这些会写入镜像的 CLI 命令要求 Redis 可用；在开始 PostgreSQL 同步前会
验证 Redis 连接，避免把 Like 初始化或删除维护失败静默报告为成功。新建
镜像 User 在 SQL 事务中初始化 User Like sentinel；已有或恢复的 User Set
不会被重置。SQL 已提交后，新 Post Like 初始化或删除清理失败会让命令返回
明确错误，提示 SQL 已提交且 Redis 维护仍待处理。

同 ID 的 DevData Post 重新激活当前保持 Fail Closed。同步在 SQL 清除
`deleted_at` 或更新镜像映射前拒绝该转换，避免旧 User Set 成员被误认为新
生命周期的 Like。此限制适用于零 Count/Version 的情况；本阶段没有可证明
从未存在旧关系的持久元数据。

## Persistent snapshot requirement

`.devdata/x_latest.json` 不是临时 cache，而是增量连续性状态以及完整的
rebuild/recovery snapshot。运行 scheduled incremental 的环境必须保证它能
跨越进程重启、scheduler 重启以及 container 重启或重建而保留。

支持的部署模型是：

- 单 scheduler host 加持久化本地文件系统；或
- 多个 scheduler runner 共同使用同一个共享持久化文件系统。

如果 `refresh-incremental` 在 container 中运行，必须把 `.devdata` 挂载到
持久化 volume；不能依赖 container-local ephemeral storage 保存
`x_latest.json`。每次 scheduler invocation 都新建 ephemeral container、且
只把 `.devdata` 放在该 container 内的部署是不安全的，因为下一次 invocation
会丢失完整 baseline。

## Standalone fetch

`fetch` 保持 DB-independent，适合手工 operator 操作、快照生成、debug 和
recovery workflow。不要故意把 standalone `fetch` 与 scheduled
`refresh-incremental` 并发安排；fetch 不参与 PostgreSQL mutation lock，
这种并发会增加快照 writer 的 check-to-rename race 风险。
