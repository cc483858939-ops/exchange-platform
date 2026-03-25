# 文章 AI 异步落库链路说明

## 背景
当前项目的文章 AI 落库已经从“整篇文章一次调用模型 -> 直接生成文章级 `summary/tags/category` -> 回写数据库”演进为链式分析。原因很直接：长文在一次性推理里容易丢信息，标签和摘要也缺少稳定的中间抽取层。

现在的实现把链路拆成固定阶段：先切 chunk，再并行抽取，再聚合，最后交给 main agent 输出文章级结果。这样可以在不改变接口和落库结构的前提下，提高长文处理的稳定性和可解释性。

## 改造目标
- 保持现有 HTTP 路由、文章创建逻辑和最终落库字段不变。
- 只重构后台异步 AI 分析链路，不新增 chunk 明细表。
- 仍然只把文章级 `summary`、`tags`、`category`、`status` 回写到 `articles` 表。
- 支持 chunk agent 和 main agent 使用不同模型配置。
- 在单篇文章内部支持受控并行的 chunk 抽取，而不是无上限 fan-out。

## 当前实现形态
当前 analyzer 是一条明确的 EINO `Chain`，固定阶段如下：

1. `splitter lambda`
2. `parallel-extract lambda`
3. `aggregate lambda`
4. `main-agent lambda`
5. `normalize/filter lambda`

这里的关键设计点是：
- 外层阶段使用 chain 表达，链路清晰。
- 运行时 chunk 数量是动态的，所以 chunk fan-out 不放进静态 `Parallel` 节点，而是放在 `parallel-extract lambda` 内部完成。
- chunk 并行通过 `errgroup + semaphore` 控制，避免 `worker 数 * chunk 数` 把模型接口瞬间打满。

## 新链路流程
### 1. 文章创建
文章创建接口仍然先把原始文章落库，并把 `status` 标记为 `pending`。随后文章 ID 会进入 Redis 的待分析集合，等待后台 worker 消费。

### 2. Worker 取任务
后台 worker 从 `article:analysis:dirty_set` 中取出文章 ID，进入处理前先把文章状态改为 `processing`，并失效文章详情缓存。

### 3. Splitter 切分文章
splitter 采用三级策略：

#### 第一级：按段落切
- 优先按空行分段，即连续空白行视为段落边界。
- 若按空行只能得到 1 段，但文本中存在普通换行，则退化为按单换行分段。
- 去掉空段，保持原顺序。

#### 第二级：超长段落按标点切
如果某个段落长度超过 `ai.chunk_size`，先尝试按标点切分：
- 句界标点：`。！？!?；;`
- 次级标点：`，,、：:`

切分规则是先按句界标点拆；如果拆出来的片段仍然过长，再按次级标点拆。标点保留在前一片段末尾，不丢字符，不重排顺序。

#### 第三级：仍然超长时按长度硬切
如果一个片段连标点都无法切到安全长度，就进入最后兜底：
- 使用 rune 级长度切分，避免中文被截坏。
- `chunk_overlap` 只在这一步生效。
- 如果 `chunk_overlap >= chunk_size`，运行时会自动降为 `0`，避免死循环。

额外约束：
- 不跨段落混装 chunk。
- 只有在同一段落内部、继续追加不会超长时，才会把相邻片段装进同一个 chunk。

### 4. Chunk Agent 并行抽取
每个 chunk 会调用 chunk agent，抽取三类结构化结果：
- `summary`
- `tags`
- `category`

chunk agent 必须只返回 JSON：

```json
{"summary":"...","tags":["..."],"category":"..."}
```

当前实现不是串行逐段调用，而是受控并行：
- 并发上限由 `ai.max_chunk_parallelism` 控制。
- 每个 chunk 的成功或失败彼此独立。
- 单个 chunk 失败会记日志并跳过，不会立刻让整篇文章失败。

### 5. 程序侧聚合
服务端会对成功 chunk 的结果做确定性聚合：
- chunk 摘要按 `ChunkIndex` 原顺序保留。
- 标签按 `trim + lowercase` 归一化计数。
- 标签展示值保留首次出现的原始写法。
- 标签候选按“出现频次降序 + 首次出现顺序升序”排序。
- `category` 也做频次统计，供 main agent 判断。

聚合结果不会落库，只作为 main agent 的输入上下文。

### 6. Main Agent 整理全文结果
main agent 只消费聚合后的上下文，不重新读取全文正文。输入包括：
- 文章标题 `title`
- 文章预览 `preview`
- 按顺序排列的 chunk 摘要
- 标签候选及其频次
- category 统计
- 成功 / 失败的 chunk 数量
- 目标标签数量 `top_n_tags`

main agent 同样只允许返回 JSON：

```json
{"summary":"...","tags":["..."],"category":"..."}
```

其中 `tags` 必须来自聚合出的候选标签集合。服务端会做二次过滤和截断，保证最终最多只落 `top_n_tags` 个标签。

### 7. 最终落库
成功后只更新文章表中的以下字段：
- `summary`
- `tags`
- `category`
- `status=completed`

如果全部 chunk 都失败，或者 main agent 失败，或者最终数据库回写失败，则文章状态会被标记为 `failed`。

## 失败策略
当前版本采用“部分成功即可继续”的策略：
- 只要至少有一个 chunk 成功，就继续执行聚合和 main agent。
- 只有在成功 chunk 数为 0 时，整篇文章才直接失败。
- 无论成功还是失败，任务都会 ACK，避免坏任务在 Redis 中反复卡住。

这意味着链路优先追求“整篇尽量有结果”，而不是因为单个 chunk 波动就整篇报废。

## 配置说明
当前 AI 配置示例如下：

```yaml
ai:
  base_url: "https://api.deepseek.com"
  api_key: "your-api-key"
  model: "deepseek-chat"
  chunk_model: "deepseek-chat"
  main_model: "deepseek-chat"
  timeout_seconds: 30
  chunk_size: 1200
  chunk_overlap: 120
  max_chunk_parallelism: 3
  top_n_tags: 5
```

字段说明：
- `model`：兼容旧配置的公共模型名。若未显式配置 `chunk_model` 或 `main_model`，会回退到这里。
- `chunk_model`：chunk agent 使用的模型。
- `main_model`：main agent 使用的模型。
- `timeout_seconds`：单次模型调用超时。
- `chunk_size`：单个 chunk 的最大 rune 长度。
- `chunk_overlap`：仅在无标点硬切时生效的重叠长度。
- `max_chunk_parallelism`：单篇文章内部允许并发处理的 chunk 数上限。
- `top_n_tags`：最终回写的标签数量上限。

## 兼容性说明
这次实现刻意保持了以下兼容边界：
- `POST /api/articles` 不变。
- Redis 任务集合和 worker 调度方式不变。
- 数据库最终字段仍然是文章级 `summary/tags/category/status`。
- `ArticleAnalyzer` 对 worker 暴露的接口不变，仍然是 `Analyze(ctx, article) -> ArticleAnalysisResult`。
- 旧配置只写 `ai.model` 时仍可运行，新版本会自动把它作为 chunk 和 main 的兜底模型。

## 主要代码落点
本次实现主要集中在以下几个位置：
- `config/config.go`：扩展 AI 配置结构，增加 `max_chunk_parallelism`。
- `config/config.yml`：补充并发控制配置示例。
- `tasks/article_analysis_agent.go`：实现 chain、splitter、并行 chunk 抽取、聚合和 main agent 整理。
- `tasks/article_analysis_agent_test.go`：覆盖 splitter、受控并行、聚合、失败语义和配置兼容。

## 测试覆盖
当前重点测试包括：
- 空行分段和单换行退化分段。
- 段落超长时按句界标点、次级标点切分。
- 无标点文本按 rune 长度硬切。
- `chunk_overlap >= chunk_size` 自动归零。
- `max_chunk_parallelism` 生效，chunk fan-out 不会超过配置上限。
- 聚合阶段的标签归一化、频次排序、TopN 截断。
- chunk 全成功、部分失败、全部失败，以及 main agent 失败。
- 只配置旧 `ai.model` 时的兼容初始化。

## 当前实现边界
当前版本有意不做以下事情：
- 不落 chunk 级中间结果。
- 不新增人工重试或补偿机制。
- 不在文章详情接口中暴露 chunk 级分析内容。
- 不把 splitter 扩展到 markdown/HTML 语义解析，当前只面向普通文本内容。

## 后续可扩展方向
如果后续继续增强，这几个方向优先级最高：
- 增加 chunk 级明细落库，用于审计和排查。
- 为 main agent 增加更强约束的结构化输出校验。
- 把 splitter 从纯文本规则升级为 markdown / HTML / 富文本感知版本。
- 为任务层加入自动重试、补偿和更细粒度的监控指标。
