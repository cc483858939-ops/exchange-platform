# Exchange Platform

一个全栈汇率与内容服务平台，包含 Go 后端服务与 Vue 前端应用。

项目提供用户认证、文章管理、文章推荐、点赞系统、汇率查询、文件存储等能力，并通过 Docker Compose 集成本地开发所需的基础设施。

## 项目组成

```
exchange-platform/
├── Go.exchange/              # Go + Gin 后端服务
├── Exchangeapp_frontend/     # Vue3 + TypeScript 前端应用
├── docker-compose.yml        # 全栈开发环境编排
└── README.md
```

## 核心功能

### 用户系统

- 用户注册
- 用户登录
- JWT Token 鉴权
- Refresh Token 刷新机制

### 文章系统

- 创建文章
- 文章列表与详情查询
- 文章封面上传
- 文章点赞与取消点赞

### 推荐系统

- 用户行为采集
- 文章推荐接口
- 推荐事件处理
- 后台异步计算

### 汇率系统

- 汇率查询
- 货币列表查询
- 汇率报价查询
- 汇率数据同步任务

### 高性能点赞架构

点赞链路采用缓存与异步处理设计：

```
Client
  |
  v
Redis
  |
  v
Worker
  |
  v
PostgreSQL
```

特点：

- Redis 承担高频点赞请求
- Worker 后台异步处理持久化
- Lua 脚本保证原子操作
- 支持批量同步和失败重试

## 技术栈

### Backend

- Go 1.25
- Gin
- GORM
- PostgreSQL
- Redis
- MinIO
- JWT
- Prometheus
- Grafana

### Frontend

- Vue 3
- TypeScript
- Vite
- Element Plus
- Pinia

### Infrastructure

- Docker
- Docker Compose
- Kafka
- Kafka UI

## 运行模式

后端支持不同运行角色：

|角色|说明|
|-|-|
|api|HTTP API 服务|
|worker|后台任务服务|
|all|API + Worker 混合运行|

通过环境变量控制：

```bash
APP_RUNTIME_ROLE=api
```

## 快速启动

### 环境要求

- Docker
- Docker Compose
- Go 1.25+ (required for first-time local JWT key generation)

### 启动完整开发环境

首次启动先在 `Go.exchange` 目录生成未跟踪的本地 JWT 密钥：

First-time local JWT key generation requires host Go 1.25+; Docker-only generation is not documented because it has not been verified.

```powershell
cd Go.exchange
go run ./cmd/gen-jwt-keys --kid local-dev-v1 --out .secrets/jwt
cd ..
docker compose up -d
```

`Go.exchange/.env.example` 只是配置参考；从项目根目录运行 Compose 时不会自动加载它。
Compose does not load `Go.exchange/.env.example` automatically. When run from the repository root, override defaults via the shell environment, root `.env`, or an explicit `--env-file`; otherwise `docker-compose.yml` local defaults apply.

启动服务包括：

- Frontend
- API
- Worker
- PostgreSQL
- Redis
- MinIO
- Kafka
- Prometheus
- Grafana

## 服务地址

|服务|地址|
|-|-|
|Frontend|http://127.0.0.1:5173|
|API|http://127.0.0.1:3000|
|API Health|http://127.0.0.1:3000/healthz|
|API Metrics|http://127.0.0.1:3000/metrics|
|Prometheus|http://127.0.0.1:9090|
|Grafana|http://127.0.0.1:3001|
|MinIO Console|http://127.0.0.1:9001|
|Kafka UI|http://127.0.0.1:8080|

## API 概览

### 认证接口

```
POST /api/auth/login
POST /api/auth/register
POST /api/auth/refresh
```

### 汇率接口

```
GET /api/exchangeRates
GET /api/exchange/currencies
GET /api/exchange/quote
```

### 文章接口（需要认证）

```
POST   /api/articles
GET    /api/articles
GET    /api/articles/:id
GET    /api/articles/:id/like
PUT    /api/articles/:id/like
DELETE /api/articles/:id/like
```

### 推荐接口（需要认证）

```
GET  /api/recommendations/articles
POST /api/recommendation-events
```

## 项目结构

### Backend

```
Go.exchange/
├── cmd/              # 命令入口
├── config/           # 配置加载
├── controllers/      # HTTP Controller
├── core/             # 服务启动与优雅退出
├── initialize/       # 初始化流程
├── metrics/          # Prometheus 指标
├── middlewares/      # JWT 中间件
├── models/           # 数据模型
├── router/           # 路由注册
├── tasks/            # 异步任务
├── utils/            # 工具类
└── main.go
```

### Frontend

```
Exchangeapp_frontend/
├── src/
│   ├── views/        # 页面
│   ├── components/   # 组件
│   ├── router/       # 路由
│   ├── services/     # API 服务
│   └── store/        # 状态管理
└── package.json
```

## 数据与基础设施

本地环境使用：

- PostgreSQL：业务数据存储
- Redis：缓存、高频状态处理
- MinIO：对象存储
- Kafka：事件基础设施

数据库迁移独立执行，不在 API 启动阶段自动修改结构。

## 开发说明

后端目录：

```bash
cd Go.exchange
```

前端目录：

```bash
cd Exchangeapp_frontend
npm install
npm run dev
```
