# Go.exchange 

Go.exchange 是一个基于 Go 语言和 Gin 框架开发的高性能后端服务项目，集成了文章管理、点赞系统（Redis 异步缓存与 MySQL 数据同步）以及汇率查询等功能。

## 🛠 技术栈

- **Web 框架**: [Gin](https://gin-gonic.com/) (v1.11.0)
- **ORM / 数据库**: [GORM](https://gorm.io/) + MySQL 8.0
- 缓存与异步: Redis (v7) + Lua 脚本实现高性能并发处理
  
- 认证授权: JWT (JSON Web Token)

- 部署环境 Docker & Docker Compose

 核心功能

1. 用户认证 (Authentication)
   - 用户注册、登录
   - JWT 令牌刷新机制 (Refresh Token)

2. 文章管理 (Article Management)
   - 发布文章
   - 获取文章列表及详情

3. 高性能点赞系统 (Like System)
   - 使用 Redis 处理海量的高频率点赞并发请求，缓解数据库压力。
   - 采用后端的异步定时任务 (`sync_likes` Worker) 将 Redis 中的点赞数据通过 Lua 脚本原子性、批量地同步持久化到 MySQL 数据库中（具有 ACK 机制及容错重试处理）。

4. 汇率功能 (Exchange Rates)
   - 支持创建和查询汇率信息。
 项目结构
Go.exchange/
├── config/           # 配置文件与配置加载
├── consts/           # 全局静态常量、Redis Key 命名规范及 Lua 脚本定义
├── controllers/      # 接口层 (登录/注册/文章/汇率等 Controller)
├── core/             # 核心组件 (HTTP 服务启动与优雅退出设计)
├── global/           # 全局变量 (数据库连接池 db、redis 等)
├── initialize/       # 系统初始化 (配置、路由、连接池装载)
├── middlewares/      # 中间件 (JWT Auth 鉴权中间件等)
├── models/           # 数据模型 (MySQL 数据库映射结构)
├── router/           # 路由配置注册中心
├── tasks/            # 异步后台持续运行任务 (如 sync_likes 将 Redis 同步至 DB)
├── utils/            # 工具类 (加密、Token 颁发等)
├── main.go           # 服务入口
└── docker-compose.yml# 依赖环境一键拉起
```

环境与依赖安装

本项目使用 Docker Compose 进行环境编排，能够一键拉起所需的外部依赖服务（MySQL、Redis、Kafka）。

1. 确保已安装 [Docker](https://www.docker.com/) 和 [Docker Compose](https://docs.docker.com/compose/)。
2. 启动基础服务（包括 mysql, redis 等）：
   docker-compose up -d
   
   > 容器提供的端口映射：
   > - MySQL: 3306 
   > - Redis: 6379, 6060



 API 接口概览

公共认证接口
- POST /api/auth/login` —— 账号登录
- POST /api/auth/register` —— 账号注册
- POST /api/auth/refresh` —— 刷新访问令牌

汇率接口
- GET /api/exchangeRates —— 查询汇率 (免鉴权)
- POST /api/exchangeRates —— 创建汇率 (需鉴权)

文章接口 (需鉴权)
- `GET /api/articles —— 获取文章列表
- `GET /api/articles/:id —— 获取文章详细信息
- `POST /api/articles —— 发布新文章
- `GET /api/articles/:id/like —— 获取文章当前点赞状态/数量
- `POST /api/articles/:id/like —— 针对文章进行点赞操作

开发与设计亮点

- 防惊群效应: 在后台动态调度任务(`tasks.dynamicLoop`)中加入了休眠退避与随机时间(Random Jitter)的机制，避免了 Redis 出现打点式的瞬时集中冲击以及协程的空转浪费。
- 原子性取值与消费: 利用 `Eval` 调用预置的 Lua 脚本批量并原子性获取待处理集合的数据，处理落库完毕后通过清理 `Processing Set` 完成业务的ACK。
- 平滑重启 (Graceful Shutdown): 主进程使用 context.WithCancel与 sync.WaitGroup进行控制，并在 core.WaitForShutdown捕获系统退出信号(SIGINT/SIGTERM)，进而做到无损切流和确保正在处理中的写库操作安全退出。
