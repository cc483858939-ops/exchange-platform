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

暂停 scheduler 不会改变快照。长时间暂停后，建议先运行一次完整 `refresh`，再
恢复每小时的四分片任务。增量刷新只会新增或更新选中账号的帖子、资料和未解析的
媒体，不会根据短源窗口推断删除，也不会退休未出现在本轮窗口中的历史帖子。

完整 refresh、rebuild 和 refresh-incremental 共用 PostgreSQL session advisory
lock；增量任务发现另一个 DevData mutation 正在运行时会安全跳过，下一小时再由
scheduler 重试。
