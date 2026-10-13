# PostgreSQL 连接预算

API、Worker 和维护命令使用独立的 `database/sql` 连接池。初始单进程上限为：

| 进程角色 | 环境变量 | 默认 MaxOpenConns | 默认 MaxIdleConns |
| --- | --- | ---: | ---: |
| API | `API_DB_MAX_OPEN_CONNS` | 20 | 2 |
| Worker | `WORKER_DB_MAX_OPEN_CONNS` | 10 | 2 |
| Maintenance | `MAINTENANCE_DB_MAX_OPEN_CONNS` | 3 | 2 |

空闲连接上限为 `min(2, MaxOpenConns)`。这些默认值用于建立明确的预算边界，尚未通过生产负载测试确定为最优值。每个进程分别读取自己的角色配置；修改一个角色不会改变其他角色。

按可能同时运行的进程计算应用连接上限：

```text
应用连接上限 =
  API 实例数 × API 单实例上限
+ Worker 实例数 × Worker 单实例上限
+ 同时运行的维护进程数 × Maintenance 单进程上限
+ 其他独立应用连接池上限
```

将应用连接上限与其他数据库客户端预算相加后，必须不超过 PostgreSQL 为这些客户端保留的容量：

```text
应用连接上限 + Debezium 等其他客户端连接预算
<= max_connections - 保留连接 - 运维安全余量
```

容量核算前，应在目标 PostgreSQL 上检查 `max_connections` 和 `superuser_reserved_connections`；PostgreSQL 版本支持时也要计入 `reserved_connections`。将 Debezium 连接器/任务、迁移进程、监控客户端和任何其他直连数据库的程序作为其他客户端逐项计数。迁移或手动维护命令虽然通常是短时进程，仍应按可能并发运行的数量计入。

默认单 VPS 拓扑应以当前运行的 API、Worker 和维护任务实例数核算。增加 API/Worker 副本或并行维护任务时，先按公式重算预算，再调整相应角色的环境变量或并发数；不要把这些单实例默认值当作多实例总额度。当前部署规模、PostgreSQL 实际连接参数及第三方客户端预算需由目标环境确认，因此本文不代表已经验证生产总连接预算。
