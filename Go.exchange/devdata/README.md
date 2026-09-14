# DevData mirror refresh

DevData 的完整快照由 `cmd/devdata` 负责生成和同步。增量刷新是一个可选的
operator/scheduler 命令，不由应用进程自动启动。

## 首次初始化和完整校准

先启动 PostgreSQL、Redis、MinIO（如需媒体镜像）以及 RSSHub，再从
`Go.exchange` 目录执行完整刷新：

```powershell
go run ./cmd/devdata refresh --source=rsshub --allow-destructive --reset-checkpoint
```

完整刷新会重建 `.devdata/x_latest.json` 的完整账号/帖子清单，并保留 RSSHub
的可恢复 checkpoint 语义。`rebuild` 只读取已有的完整快照，不会重新抓取源数据。

## 每小时增量刷新

外部 scheduler 可以每小时执行一次：

```text
0 * * * * cd /path/to/repository/Go.exchange && go run ./cmd/devdata refresh-incremental --source=rsshub --shard=auto
```

启用账号按稳定的 registry key 分成 4 个固定分片。`--shard=auto` 使用 UTC
小时桶选择分片，因此每个账号约每 4 小时刷新一次。也可以手动运行
`--shard=0` 到 `--shard=3`；同一小时重复运行同一分片是幂等的。

默认每个账号先请求最近 20 条源帖子；窗口恰好填满且和完整基线没有重叠时，
才回退请求 60 条。`DEVDATA_INCREMENTAL_FETCH_COUNT` 或 `--fetch-count` 可
将初始窗口设置为 5 到 60 之间的值。遇到 RSSHub 429 时本轮立即停止，不会额外
重试，也不会写入部分增量快照。

增量刷新要求 `.devdata/x_latest.json` 是与当前 registry 和 eligibility policy
匹配的完整有效基线。缺失、损坏或过期时，应先运行一次完整 `refresh`；增量命令
不会自动删除或重置快照，也不接受 `--allow-destructive` 或
`--reset-checkpoint`。

## 暂停、恢复和定期校准

暂停 scheduler 不会改变快照。短暂停顿通常可以直接恢复增量任务；长时间或
不确定时长的暂停，建议先运行一次完整 `refresh`，再恢复每小时的四分片任务。
源可见性受每个账号最多 60 条的窗口限制，不能承诺增量任务无限补齐暂停期间的
历史帖子。增量刷新只会新增或更新选中账号的帖子、资料和未解析的媒体，不会
根据短源窗口推断删除，也不会退休未出现在本轮窗口中的历史帖子。

## Production scheduling

推荐把两个命令看成不同的运维职责：

- `refresh-incremental` 负责 freshness。每小时运行一次、每次一个分片，
  因此每个账号约每 4 小时刷新一次。
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
本次 invocation；下一小时的 scheduler 会继续，不需要 operator 手工恢复。

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
