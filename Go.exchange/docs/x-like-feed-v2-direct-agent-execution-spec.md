# Exchange Platform X-like Feed v2

## Direct Agent Execution Spec

本文是一份可直接交给实现 Agent 执行的最终规范。

目标不是复制 X 当前的大规模 ML 基础设施，而是在现有 Go、PostgreSQL、pgvector、Vue 架构上实现一套规则驱动、可解释、可观测、可逐步演进的 X-like Feed。

本文冻结产品合同、信号语义、召回来源、排序公式、网络内外混排、多样性、近期展示去重、数据库结构、测试与验收。Agent 不得在执行过程中自行替换这些合同。

---

# 0. 执行模式

## 0.1 Repository

    Git root:
    D:\code\mf

    Backend:
    D:\code\mf\Go.exchange

    Frontend:
    D:\code\mf\Exchangeapp_frontend

    Expected remote:
    https://github.com/cc483858939-ops/mf.git

    Base:
    main

执行前必须运行：

    git rev-parse --show-toplevel
    git branch --show-current
    git rev-parse HEAD
    git remote -v
    git status --short

远端、Git root 或目标仓库不一致时停止并报告，不得把本文应用到另一个仓库。

## 0.2 工作树保护

- 保留所有无关修改和未跟踪文件。
- 不得 reset、restore、checkout、clean 或覆盖用户工作。
- 不得自动 commit、push、rebase 或创建 PR。
- 每阶段检查 git status、changed names 和 git diff。
- 允许文件已有用户修改时，在其基础上最小化编辑。

## 0.3 Deliverables

必须完成：

- 后端实现。
- 数据库 migration。
- 配置与归一化。
- 单元测试。
- PostgreSQL 集成测试。
- Prometheus metrics。
- Request/Result serving trace。
- Trace retention cleanup task。
- 文档与完整验证。

此次不进行架构重写，也不等待人工从多个候选方案中选择。

---

# 1. 产品合同

## 1.1 Following

Following 是关注时间线：

    只包含已关注作者的公开文章
    published_at DESC
    article_id DESC

Following 不进行语义排序、网络外内容插入或 For You diversity。

现有 Following API 和响应合同保持不变。

## 1.2 For You

For You 混合：

    In-network:
    已关注作者内容

    Out-of-network:
    未关注作者的相关、近期和热门内容

For You 必须拥有：

    Positive Interest Profile
    Negative Interest Profile

    Semantic Recall
    Following Recall
    Recent Recall
    Popular Recall

    Full-history Not Interested Suppression
    Interacted Exclusion
    Recently Served Exclusion
    Self-authored Exclusion

    Semantic Score
    Negative Semantic Penalty
    Freshness
    Popularity
    Author Affinity
    Following Bonus

    In/Out Network Balance
    Novel Author Discovery
    Author Diversity
    Semantic Duplicate Penalty

    Request Trace
    Result Trace

## 1.3 Pipeline

    Raw behavior and feedback
        ->
    Per-article canonical outcome
        ->
    PositiveVector + NegativeVector + NegativeConfidence
        ->
    Semantic + Following + Recent + Popular recall
        ->
    Eligibility and suppression
        ->
    Merge and batch hydration
        ->
    Base scoring
        ->
    Network balance + discovery + diversity
        ->
    Response + signed tracking
        ->
    Best-effort request/result trace

## 1.4 Success

- Following 与 For You 语义清晰分离。
- For You 同时有网络内和网络外候选。
- 负反馈不再反向污染 PositiveVector。
- 单篇文章不能因多事件无限重复加权。
- Not Interested 只有更晚明确正反馈才能解除。
- 刷新不会立刻重复刚展示文章。
- 候选来源、得分和选择可追踪。
- 候选不足允许有界放宽，但不绕过可见性、明确负反馈和 hard served。
- 相同输入和相同 request ID 下结果确定。

---

# 2. 当前基线

本文编写时：

    branch:
    main

    HEAD:
    9f90499

已有：

- Article、ArticleEmbedding、ArticleEmbeddingJob、pgvector。
- ArticleReaction、ArticleBehavior。
- recommendation events。
- signed tracking token v2。
- read_v1。
- read_end_recency_v2。
- RecommendationRequest。
- Following 独立时间线。
- publicArticleScope。

当前 For You：

    Single User Vector
        ->
    Semantic + Recent + Popular
        ->
    Semantic + Freshness + Popularity

当前缺少：

- Following Recall 进入 For You。
- 正负画像分离。
- Reply 推荐信号。
- Author Affinity。
- 网络内外平衡。
- 新作者发现。
- Author Diversity。
- Semantic duplicate penalty。
- Recently served exclusion。
- RecommendationResultTrace。

Agent 以执行时实际源码适配文件位置，但不得改变本文合同。

---

# 3. Scope

## 3.1 In scope

- Reply ArticleBehavior。
- Capped multi-signal facts。
- PositiveVector、NegativeVector、NegativeConfidence。
- 四源召回。
- Full-history NI suppression。
- Hard/soft served exclusion。
- Source attribution。
- Author affinity 和 follow bonus。
- Like + comment popularity。
- Network balance 和 novel-author discovery。
- Author 与 semantic diversity。
- Request/Result trace 与 retention cleanup。
- Metrics、migration、测试和验收。

## 3.2 Out of scope

禁止：

- Transformer、LLM、ML ranker、LTR、Cross Encoder。
- Persistent user vector、multi-interest vector。
- HNSW、IVFFlat。
- 外部 vector DB。
- 2-hop graph、community recommendation。
- Repost、Quote Post、thread redesign。
- 新建 block/mute 或 topic-follow 系统。
- Article 到 Post 全局改名。
- recommendation package 全量搬迁。
- Embedding provider、worker、job 重构。
- API schema redesign。
- 前端视觉重设计。

---

# 4. 不可破坏合同

- publicArticleScope 是公开资格唯一来源。
- 未来发布、过期、删除文章不得进入 Feed。
- 无效或删除作者的文章不得进入 Feed。
- Following API 保持独立。
- read_v1 不变。
- tracking token version 保持 v2。
- token 继续绑定 estimated_read_time_ms 和 read_policy_version。
- read_end_recency_v2 不变。
- ArticleReaction 是 Like/Unlike 当前状态真相。
- StateChangedAt 使用行为发生时间。
- response 保留 score 和 tracking。
- client telemetry 与 serving trace 语义分离。
- trace 持久化失败不得让 API 返回 500。

---

# 5. Versions

    RankerVersion:
    rules_v2

    StrategyID:
    for_you_rules_v2

    CandidateRetrievalVersion:
    social_semantic_multi_source_v2

    CanonicalOutcomeVersion:
    multi_signal_capped_v2

    SelectionPolicyVersion:
    network_balance_diversity_v1

    PassiveRecencyPolicy:
    read_end_recency_v2

    ReadPolicy:
    read_v1

    TrackingTokenVersion:
    v2

不得保留 embedding_v1 ranker 的运行时 fallback。

不得同时维护两套 rank formula。

---

# 6. Configuration

修改：

    config/config.go
    config/config.yml

## 6.1 Defaults

    recommendation:
      behavior_weights:
        view: 0.5
        click: 1.5
        qualified_read: 3
        reply: 5
        like: 6
        quick_bounce: -3
        not_interested: -6

      signal_half_life_days: 14
      feedback_lookback_days: 90

      positive_signal_coexist_bonus: 1
      positive_article_weight_cap: 7

      semantic_weight: 4
      negative_semantic_weight: 1.5
      negative_confidence_saturation_scale: 12

      freshness_weight: 2
      freshness_half_life_days: 2

      popularity_weight: 0.5
      popularity_comment_factor: 0.5

      author_affinity_weight: 1
      author_affinity_saturation_scale: 6
      following_bonus: 0.5

      out_of_network_min_ratio: 0.30
      novel_author_min_ratio: 0.10

      served_hard_exclusion_minutes: 30
      served_soft_lookback_days: 7
      served_history_limit: 1000

      diversity:
        enabled: true
        author_window_size: 8
        max_same_author_in_window: 2
        semantic_duplicate_threshold: 0.92
        semantic_duplicate_penalty: 1

      trace:
        result_retention_days: 30
        request_retention_days: 90
        cleanup_interval_hours: 6
        cleanup_batch_size: 5000

neutral_read 固定权重 0。

删除原计划 ExplorationRatio，使用真实网络分类与新作者发现目标。

## 6.2 Candidate caps

    personalized:
      semantic: 200
      following: 150
      recent: 150
      popular: 150
      merged: 500

    semantic cold start:
      following: 200
      recent: 200
      popular: 200
      merged: 500

所有值集中定义，不得散落 magic number。

## 6.3 Normalization

规则：

    Reply >= 0
    PositiveSignalCoexistBonus >= 0
    PositiveArticleWeightCap > 0
    PositiveArticleWeightCap >= max(Like, Reply)

    NegativeSemanticWeight >= 0
    NegativeConfidenceSaturationScale > 0

    AuthorAffinityWeight >= 0
    AuthorAffinitySaturationScale > 0
    FollowingBonus >= 0

    PopularityCommentFactor >= 0

    OutOfNetworkMinRatio in [0,1]
    NovelAuthorMinRatio in [0,1]
    NovelAuthorMinRatio <= OutOfNetworkMinRatio

    ServedHardExclusionMinutes > 0
    ServedSoftLookbackDays > 0
    ServedHistoryLimit > 0

    AuthorWindowSize > 0
    MaxSameAuthorInWindow > 0
    SemanticDuplicateThreshold in [-1,1]
    SemanticDuplicatePenalty >= 0

    ResultRetentionDays > 0
    RequestRetentionDays >= ResultRetentionDays
    CleanupIntervalHours > 0
    CleanupBatchSize > 0

无效值回退本文默认值。

可将 normalizedEmbeddingRecommendationConfig 重命名为 normalizedRecommendationConfig；若重命名，删除旧 helper，不留 wrapper。

---

# 7. Reply behavior

## 7.1 Constant

在现有 action constants 位置增加：

    ArticleBehaviorActionReply = "reply"

不得新增 reply table、Kafka topic、outbox event 或 projection consumer。

## 7.2 Atomic write

修改 comment_store.go。

    BEGIN
    verify public article
    INSERT comment
    increment comment_count
    UPSERT reply ArticleBehavior
    COMMIT

Reply behavior 必须与 comment 和 comment_count 同事务。

## 7.3 UPSERT

唯一键继续使用：

    user_id
    article_id
    action

首次：

    count = 1
    last_seen_at = now
    active = true

后续：

    count = count + 1
    last_seen_at = GREATEST(existing, incoming)
    active = true

必须数据库原子 UPSERT，禁止 SELECT 再 UPDATE。

并发回复最终必须只有一条 behavior，count 正确。

## 7.4 Delete

删除评论不删除、不递减、不停用 reply behavior。

Reply 表示历史明确兴趣。

Reply count 不参与推荐；一篇文章回复 1 次或 10 次都只贡献一个 latest reply signal。

---

# 8. Canonical per-article outcome

## 8.1 Structures

    userArticleSignal:
      SignalType
      OccurredAt

    userArticleOutcome:
      ArticleID
      PositiveSignals
      NegativeSignal
      PassiveSignal

PositiveSignals 保存仍有效的明确正信号。

NegativeSignal 每篇最多一个。

PassiveSignal 可用于诊断；存在明确正信号时不贡献画像。

## 8.2 Raw facts

每篇文章加载：

- latest view。
- latest click。
- latest read_end。
- current ArticleReaction。
- latest reply。
- latest not_interested。

View 继续受 recent-view cap 限制。

Click、read_end、NI 先按 article + event type 取最新，再跨类型比较。

NI 候选压制不得受 FeedbackLookbackDays 限制。

## 8.3 Passive recency

保持 read_end_recency_v2：

- 严格更晚 click 替代 read_end。
- 否则严格更晚 view 替代 read_end。
- click/view 都更晚时 click 胜。
- 无 read_end 时 click 胜过 view。
- 时间相同保留 read_end。

read_end 映射为：

    qualified_read
    quick_bounce
    neutral_read

## 8.4 Not Interested precedence

设：

    NI = latest not_interested
    L = current liked StateChangedAt
    R = latest reply

有效 Like：

    liked == true
    and
    NI absent or L > NI

有效 Reply：

    reply exists
    and
    NI absent or R > NI

如果有 NI 且没有更晚有效 Like/Reply：

    NegativeSignal = not_interested
    PositiveSignals = empty
    PassiveSignal does not contribute

Unlike 不解除 NI。

Click、View、Read 不解除 NI。

相同时间 NI 胜出。

## 8.5 Explicit positives

没有有效 NI 压制时：

- current Like 加入 PositiveSignals。
- latest Reply 加入 PositiveSignals。
- Like 与 Reply 可以同时保留事实。

只要存在明确正信号：

- passive 不贡献 PositiveVector。
- quick bounce 不产生 NegativeSignal。
- 文章只产生一次 embedding contribution。

## 8.6 Quick bounce and neutral

只有没有 Like、Reply、NI，且 canonical passive 为 quick_bounce 时：

    NegativeSignal = quick_bounce

优先级：

    not_interested > quick_bounce

neutral_read：

- weight = 0。
- 不贡献正负向量。
- 仍加入 InteractedArticleIDs。

---

# 9. Per-article contribution cap

Like + Reply 不得直接计算 6 + 5。

对每个明确正信号：

    decayed_i =
        configured_weight_i
        * time_decay(signal_time_i)

多个明确正信号：

    primary = max(decayed signals)

    coexist_bonus =
        positive_signal_coexist_bonus
        * decay(other signal time)

    positive_strength =
        min(
            positive_article_weight_cap,
            primary + coexist_bonus
        )

单个明确正信号：

    positive_strength =
        min(cap, decayed_signal)

只有 passive positive：

    positive_strength =
        passive_weight * time_decay

默认最大：

    Like only: 6
    Reply only: 5
    Like + Reply: 7

多次 Reply 不增加 contribution。

---

# 10. User interest profile

## 10.1 Structure

    PositiveVector
    NegativeVector
    NegativeConfidence

    InteractedArticleIDs

    PositiveSignalCount
    NegativeSignalCount
    PersonalizedSignalCount

删除旧 Vector 所有运行时使用。

PersonalizedSignalCount 计 distinct contributing articles。

## 10.2 PositiveVector

每篇有效 positive_strength：

    contribution =
        positive_strength * article_embedding

求和后 L2 normalize。

缺 embedding、维度不一致、NaN、Inf、零范数：

- 加入 InteractedArticleIDs。
- 不贡献向量。
- 不增加 contributing count。
- 不让整个请求失败。

## 10.3 NegativeVector

只处理 quick_bounce 和 not_interested。

    negative_strength =
        abs(configured_weight) * time_decay

    contribution =
        negative_strength * article_embedding

求和后 L2 normalize。

绝对禁止：

    PositiveVector += negative_weight * embedding

## 10.4 NegativeConfidence

单个 quick bounce 不得形成满强度惩罚。

    negative_evidence =
        sum(abs(weight) * time_decay)

    NegativeConfidence =
        tanh(
            negative_evidence
            /
            negative_confidence_saturation_scale
        )

范围 0..1。

NegativeVector 为空时 confidence = 0。

NegativeSignalCount 只计实际贡献的 distinct articles。

## 10.5 InteractedArticleIDs

加入：

- view、click。
- qualified、neutral、quick-bounce read。
- not_interested。
- ArticleReaction 相关文章。
- reply。

缺 embedding 仍排除。

NI 长期压制另行执行，不能只依赖近期 InteractedArticleIDs。

---

# 11. Eligibility

所有来源共用一个 eligibility contract：

- publicArticleScope。
- author_id != current user。
- 不在当前响应已选。
- 不在 InteractedArticleIDs。
- 不被 full-history NI 压制。
- 不在 hard served。
- fresh 第一轮不在 soft served。

不得为四个来源实现不同公开语义。

Full-history NI 只有更晚 active Like 或 Reply 解除。

必须批量或子查询完成，禁止逐 candidate 查询。

For You 不推荐当前用户自己的文章。

Following API 不额外改变。

---

# 12. Recently served history

## 12.1 Load

每个请求批量加载：

    recommendation_requests
    JOIN recommendation_result_traces

条件：

    request.user_id = current user
    result.created_at >= now - soft lookback
    ORDER BY result.created_at DESC, position ASC
    LIMIT served_history_limit

按 article 保留 latest served time。

服务端成功生成并返回即视为 served，不依赖客户端 impression。

## 12.2 Hard

最近 hard exclusion minutes 内：

- 本请求绝不返回。
- 候选不足也不放宽。

## 12.3 Soft

hard window 之外、soft lookback 之内：

- fresh 第一轮排除。
- 只有 fresh eligible candidates 耗尽且不足 limit 时允许 fallback。
- 最久未展示优先。
- 仍必须通过公开、self、interacted、NI、当前响应去重。
- 仍应用 ranking 和 diversity。

Soft fallback candidate pool 必须在 fresh selection 不足时显式生成：

- 重新执行四个 source loader。
- 查询限制为 soft-served article IDs。
- 不复用旧 trace 中可能已经过期的 source flags 或 score。
- 使用当前 PositiveVector、follow set、publication state、popularity 和 embedding 重新计算。
- 每个 source 仍受自己的 candidate cap 限制。
- merge 后设置 WasSoftServed 和 LastServedAt。

不得直接把全部历史 trace rows 当作当前合法候选。

## 12.4 Failure

served history 加载失败：

- log。
- metric++。
- 使用空 served sets。
- API 继续。

---

# 13. Candidate type and sources

## 13.1 Candidate

    ArticleID
    PositiveSemanticSimilarity

    FromSemantic
    FromFollowing
    FromRecent
    FromPopular

    WasSoftServed
    LastServedAt

删除或完整重命名旧 SemanticSimilarity。

同一 ID 只出现一次，source flags 可多选。

## 13.2 Semantic

只使用 PositiveVector。

PositiveVector nil 时跳过。

NegativeVector 不用于召回。

要求：

- active embedding version。
- dimensions match。
- unified eligibility。
- cosine ascending。
- cap 200。

## 13.3 Following

    JOIN user_follows uf
      ON uf.following_id = articles.author_id

    WHERE uf.follower_id = current_user_id

应用统一 eligibility。

排序：

    published_at DESC
    id DESC

## 13.4 Recent

排序必须改为：

    published_at DESC
    id DESC

不得使用 created_at。

## 13.5 Popular

    popularity_raw =
        log(1 + max(like_count, 0))
        +
        popularity_comment_factor
        * log(1 + max(comment_count, 0))

排序：

    popularity_raw DESC
    published_at DESC
    id DESC

本版本不使用 view_count。

## 13.6 Cold start

PositiveVector nil：

- skip semantic。
- following/recent/popular 继续。

有 follows 的无行为用户仍有 social personalization。

无行为无 follows 使用 recent + popular。

---

# 14. Merge and hydration

Personalized merge：

    semantic
    following
    recent
    popular

Semantic cold start：

    following
    recent
    popular

同 ID：

- 只保留一次。
- 合并全部 flags。
- 保留 semantic similarity。
- 保留 soft served time。

Merged cap = 500。

Source counts 是各 loader distinct IDs；MergedCount 是 merge 后 distinct IDs。

Merge 后批量加载：

- Article。
- Author。
- active embedding。

Ranking 前拥有：

    ArticleID -> Article
    ArticleID -> AuthorID
    ArticleID -> Embedding

不得逐 candidate 查询。

hydrate 后再次应用 publicArticleScope。

缺失或已失效文章丢弃。

---

# 15. Negative similarity

对 merged candidate embeddings 批量计算：

    negative_similarity =
        cosine(candidate_embedding, NegativeVector)

以下情况设 0：

- embedding missing。
- NegativeVector nil。
- dimension mismatch。
- NaN/Inf。

Positive/Negative similarity clamp 到 [-1,1]。

负 similarity 不产生奖励，惩罚只使用 max(negative_similarity, 0)。

---

# 16. Author affinity

使用 canonical outcomes。

一次 batch query：

    interacted article IDs -> article_id + author_id

只使用：

    click
    qualified_read
    reply
    like

不使用：

    view
    neutral_read
    quick_bounce
    not_interested

每篇文章只贡献 capped positive strength。

    raw[author_id] += author_positive_strength

    interaction_affinity =
        tanh(
            raw
            /
            author_affinity_saturation_scale
        )

一次查询 candidate author follow set，禁止 N+1。

    author_score = interaction_affinity

followed 时：

    author_score += following_bonus

最后 clamp 0..1。

Novel author：

    not followed
    and
    no positive author-affinity history

它表示对当前用户尚无正向作者历史，不表示作者注册时间新。

---

# 17. Score breakdown and formula

## 17.1 Structure

    PositiveSemantic
    NegativeSemantic
    NegativeConfidence

    InteractionAffinity
    FollowingBonusApplied

    SemanticComponent
    FreshnessComponent
    PopularityComponent
    AuthorAffinityComponent

    BaseScore
    DiversityPenalty
    FinalScore

response、trace、selection 共用该结构，不得重复公式。

## 17.2 Semantic

    semantic_raw =
        clamp(positive_similarity, -1, 1)
        -
        negative_semantic_weight
        * NegativeConfidence
        * max(clamp(negative_similarity, -1, 1), 0)

    SemanticComponent =
        semantic_weight * semantic_raw

非 semantic 来源的 candidate PositiveSemantic = 0，但有 embedding 时仍受 negative penalty。

## 17.3 Freshness

使用 PublishedAt，CreatedAt 仅防御性 fallback。

    age_days = max(now - article_time, 0)

    freshness =
        exp(
            -ln(2)
            * age_days
            / freshness_half_life_days
        )

    FreshnessComponent =
        freshness_weight * freshness

## 17.4 Popularity

    popularity =
        log(1 + max(like_count, 0))
        +
        popularity_comment_factor
        * log(1 + max(comment_count, 0))

    PopularityComponent =
        popularity_weight * popularity

## 17.5 Author

    AuthorAffinityComponent =
        author_affinity_weight * author_score

## 17.6 Base

    BaseScore =
        SemanticComponent
        + FreshnessComponent
        + PopularityComponent
        + AuthorAffinityComponent

Tie break：

    BaseScore DESC
    PublishedAt DESC
    ArticleID DESC

---

# 18. Network balance and discovery

不实现随机 ExplorationRatio。

实现：

    Out-of-network minimum
    Novel-author minimum

实际 follow set 判定网络分类，不能用 source flag 替代。

    out_target =
        round(limit * out_of_network_min_ratio)

    novel_target =
        round(limit * novel_author_min_ratio)

clamp 0..limit，且 novel_target <= out_target。

实现：

    balancedPositions(limit, target)

要求：

- 1-based。
- deterministic。
- 均匀。
- target 0 返回空。
- target >= limit 返回全部。

推荐：

    round(k * limit / target)
    k = 1..target

冲突修正到最近未占合法位置。

Novel position：

    prefer novel
    fallback any out-of-network
    fallback any

Out position：

    prefer any out-of-network
    fallback any

Normal：

    highest selection score

Novel 同时计入 out target。

quota 无法满足时不得缩短 Feed。

这是真实发现：作者在网络外且没有既有 affinity；Recent/Popular source 本身不等于探索。

---

# 19. Diversity

最终不能 base sort 后直接 slice。

使用 greedy selection。

## 19.1 Author window

检查已选最近 author_window_size 条。

候选作者已达到 max_same_author_in_window 时第一轮不可选。

如果当前 slot 优先和 fallback pools 都无合法候选，才放宽 author rule。

不得因此缩短 Feed。

## 19.2 Semantic duplicate

候选与所有已选且有 embedding 的结果计算最大 cosine。

    if max_similarity >= threshold:
        DiversityPenalty = configured penalty
    else:
        DiversityPenalty = 0

无 embedding 时 penalty 0。

    selection_score =
        BaseScore - DiversityPenalty

Tie break：

    BaseScore DESC
    PublishedAt DESC
    ArticleID DESC

选中后：

    FinalScore =
        BaseScore - DiversityPenalty

禁止预计算完整 500 x 500 矩阵。

允许 candidate_count * limit * selected_count。

---

# 20. Fresh and soft selection

第一轮只选择 fresh candidates，执行完整网络平衡、发现和 diversity。

达到 limit 即结束。

不足时第二轮加入 soft-served candidates：

- LastServedAt ASC 优先。
- 再应用 score、slot pools、diversity。
- 标记 WasSoftServedFallback。

hard served 永不进入。

最终：

- no duplicate IDs。
- 足够 eligible fresh + soft 时 len = limit。
- quota 不让 Feed 变短。

---

# 21. RecommendationResultTrace

新增：

    models/recommendation_result_trace.go

字段：

    RequestID
    Position

    ArticleID
    AuthorID

    FromSemantic
    FromFollowing
    FromRecent
    FromPopular

    IsInNetwork
    IsNovelAuthor
    WasSoftServedFallback

    PositiveSemantic
    NegativeSemantic
    NegativeConfidence

    InteractionAffinity
    FollowingBonusApplied

    SemanticComponent
    FreshnessComponent
    PopularityComponent
    AuthorAffinityComponent

    DiversityPenalty
    BaseScore
    FinalScore

    CreatedAt
    ExpiresAt

Keys：

    composite primary key:
    request_id, position

    unique:
    request_id, article_id

Foreign keys：

    request_id -> recommendation_requests.request_id
    ON DELETE CASCADE

    article_id -> articles.id
    ON DELETE CASCADE

Indexes：

- article_id。
- created_at。
- expires_at。

Trace 不存文章正文、embedding、token、原始事件 payload。

---

# 22. RecommendationRequest extensions

保留现有字段并增加：

    SemanticCandidateCount
    FollowingCandidateCount
    RecentCandidateCount
    PopularCandidateCount
    MergedCandidateCount

    PositiveSignalCount
    NegativeSignalCount

    InNetworkResultCount
    OutOfNetworkResultCount
    NovelAuthorResultCount
    SoftServedFallbackCount

    PersonalizationMode

所有 count >= 0。

PersonalizationMode：

    semantic_social
    social_only
    cold_start

定义：

    semantic_social:
    PositiveVector exists

    social_only:
    PositiveVector nil and following candidates exist

    cold_start:
    no PositiveVector and no following candidates

FallbackReason 最终允许：

    ""
    no_positive_profile
    insufficient_fresh_candidates

删除旧运行时 fallback reason 假设，并更新数据库 check。

---

# 23. Persistence and retention

替换单独 persistRecommendationRequest 为：

    persistRecommendationServingTrace(
        request,
        results
    )

内部：

    BEGIN
    INSERT recommendation_request
    BATCH INSERT result traces
    COMMIT

必须 batch insert，禁止逐结果 INSERT。

调用发生在 response serialization 前，但必须设置有界数据库超时。

失败：

- log request ID。
- metric++。
- API 仍返回生成结果。

失败时 served history 可能缺失，允许质量降级。

## 23.1 Cleanup task

新增：

    tasks/recommendation_trace_cleanup.go

注册到：

    tasks.StartAll

每 cleanup interval：

1. 分批删除 ExpiresAt <= now 的 result traces。
2. 分批删除 CreatedAt 超过 request retention 的 requests。
3. request 删除通过 cascade 删除残留 results。

每批最多 cleanup batch size。

循环有界；一次 tick 不得无限占用数据库。

支持 context cancellation。

cleanup 失败：

- log。
- metric++。
- worker 继续。

不得把 cleanup 单次失败视为 worker fatal。

---

# 24. Metrics

增加 bounded-label metrics。

## 24.1 Recall

    recommendation_recall_candidates_total

label source：

    semantic
    following
    recent
    popular
    merged

每请求增加本次 distinct candidate 数。

## 24.2 Results by source

    recommendation_results_by_source_total

label source：

    semantic
    following
    recent
    popular

多来源文章每个真实来源都 +1。

## 24.3 Result class

    recommendation_results_by_class_total

label class：

    in_network
    out_of_network
    novel_author
    soft_served_fallback

Novel 同时是 out-of-network，两者均可 +1。

## 24.4 Quality path

增加：

    recommendation_served_history_load_failures_total
    recommendation_trace_persist_failures_total
    recommendation_trace_cleanup_failures_total
    recommendation_trace_cleanup_rows_total

不得使用 user ID、article ID、request ID 等高基数 label。

---

# 25. API and tracking

For You response schema 保持不变。

保留：

    score
    tracking

response score 使用 FinalScore。

tracking 保留：

    request_id
    position
    scene
    ranker_version
    ranker_config_hash
    strategy_id
    token
    expires_at

token claims 和 token version v2 保持。

只更新 ranker、strategy 和 config hash 内容。

不得增加 debug endpoint 或 debug query parameter。

---

# 26. Ranker config hash

canonical config string 必须包含：

- 所有 behavior weights，包括 reply。
- coexist bonus 和 article cap。
- negative semantic weight 和 confidence saturation。
- freshness、popularity、comment factor。
- author affinity、saturation、follow bonus。
- out-of-network 和 novel ratios。
- served history 参数。
- diversity 参数。
- trace retention 不影响 ranking 时可以不进入 ranker hash，但必须进入 operational config logging。
- candidate caps。
- candidate retrieval version。
- canonical outcome version。
- passive recency policy。
- read policy。
- selection policy version。
- active embedding version。

任一 ranking、retrieval、canonical 或 selection 参数变化，hash 必须变化。

---

# 27. File boundaries

## 27.1 Existing files allowed

    config/config.go
    config/config.yml

    controllers/article_behavior.go
    controllers/comment_store.go
    controllers/recommendation_controller.go
    controllers/recommendation_embedding.go
    controllers/recommendation_tracking.go
    controllers/recommendation_request_persistence.go

    models/recommendation_request.go

    initialize/migrate.go

    metrics/metrics.go
    metrics/metrics_test.go

    tasks/manager.go

    docs/recommendation-telemetry-v2.md

以及这些生产文件对应的 test files。

## 27.2 New files allowed

建议：

    controllers/recommendation_signal_v2.go
    controllers/recommendation_profile_v2.go
    controllers/recommendation_following_retrieval.go
    controllers/recommendation_candidate_v2.go
    controllers/recommendation_author_affinity_v2.go
    controllers/recommendation_ranker_v2.go
    controllers/recommendation_selection_v2.go
    controllers/recommendation_trace.go

    models/recommendation_result_trace.go

    tasks/recommendation_trace_cleanup.go

    docs/recommendation-feed-v2.md

对应 test files 可新增。

全部 controllers 文件继续：

    package controllers

不得创建新的 recommendation domain package。

## 27.3 Frontend

本任务不应修改前端生产代码。

只有在现有 API response contract 被实现意外破坏、必须修复类型或测试时才允许最小修改，并必须在最终报告说明原因。

---

# 28. Remove obsolete v1 runtime

完成后删除：

- single Vector profile。
- negative weight 直接加进单向量。
- embedding_v1 rank formula。
- old candidate flags assumptions。
- old source merge assumptions。
- old version constants。

保留历史数据库记录。

不得保留 v1/v2 双运行路径。

不得为回滚创建 feature flag。

项目未上线，本任务交付一个最终合同。

---

# 29. Migration

AutoMigrate 增加：

    RecommendationResultTrace

扩展：

    RecommendationRequest

新增显式幂等 constraint/index helper：

- composite primary key。
- request/article unique。
- request FK cascade。
- article FK。
- count checks。
- PersonalizationMode check。
- FallbackReason check。
- trace time indexes。

如果 GORM AutoMigrate 不能可靠修改已有 check，必须显式：

    DROP CONSTRAINT IF EXISTS
    ADD CONSTRAINT

项目未上线：

- 不做 legacy dual-read。
- 不做业务 backfill job。
- 新增 non-null count 使用 default 0。
- 不删除数据库 volume。

Migration 必须在同一个 disposable PostgreSQL 上连续执行两次成功。

---

# 30. Test matrix

## 30.1 Config

- 每个新默认值。
- 每个非法值回退。
- ratio 边界 0/1。
- novel <= out。
- retention relationship。
- ranker hash 对每个排名相关字段敏感。

## 30.2 Reply

- create comment 同时插入 reply behavior。
- comment_count +1。
- 第二次 reply 同一 behavior，count +1。
- last_seen_at monotonic。
- 并发 reply 不重复 row。
- comment insert 失败时 behavior 不存在。
- count update 失败时全部 rollback。
- delete comment 保留 reply。

## 30.3 Canonical outcome

覆盖：

    view only
    click only
    qualified only
    neutral only
    quick bounce only

    like only
    reply only
    like + reply

    NI only
    view -> NI
    reply -> NI
    NI -> reply
    NI -> like
    NI -> unlike
    NI -> reply + like

    equal timestamp NI vs like
    equal timestamp read_end vs click/view
    later click/view vs stale read_end

## 30.4 Contribution cap

- Like max 6。
- Reply max 5。
- Like + Reply max 7。
- multiple replies 不增加。
- 不同时间 decay。
- cap 生效。

## 30.5 Profiles

- positive only。
- negative only。
- mixed。
- single quick bounce confidence < 1。
- multiple negatives confidence increases and clamps。
- missing embedding。
- dimension mismatch。
- zero norm。
- NaN/Inf。
- distinct article counts。

## 30.6 Eligibility

- public included。
- deleted excluded。
- expired excluded。
- future unpublished excluded。
- missing published excluded。
- deleted author excluded。
- self-authored excluded。
- interacted excluded。
- old NI still suppressed beyond feedback lookback。
- later like restores。
- later reply restores。
- later unlike does not restore。
- no N+1。

## 30.7 Recall

Semantic：

- PositiveVector only。
- nil vector skips。
- active embedding version。
- cap。

Following：

- followed included。
- non-followed not FromFollowing。
- published order。
- all eligibility rules。

Recent：

- published_at, not created_at。

Popular：

- like and comment formula。
- deterministic tie break。

## 30.8 Merge

- each source alone。
- semantic + recent。
- following + recent。
- semantic + following + popular。
- all flags preserved。
- one ID only。
- cap。

## 30.9 Served history

- never served is fresh。
- within hard never returns。
- soft excluded first pass。
- soft reintroduced only on shortage。
- oldest soft first。
- soft still respects NI/interacted/self/public。
- trace load failure is nonfatal and metered。

## 30.10 Negative semantic

Candidate A：high positive, low negative。

Candidate B：same positive, high negative。

要求 A > B。

另外：

- negative similarity < 0 无 bonus。
- confidence 0 无 penalty。
- one bounce penalty weaker than saturated negative history。

## 30.11 Author affinity

- click/qualified/reply/like contribute。
- view/neutral/negative do not。
- Like + Reply capped。
- time decay。
- follow bonus。
- unrelated author 0。
- score clamp <= 1。
- novel classification。

## 30.12 Network balance

覆盖 limit：

    1
    5
    20
    50

ratio：

    0
    default
    1

验证：

- target。
- positions deterministic。
- novel counts as out。
- quota fulfilled when pool enough。
- graceful fallback when insufficient。
- no shortened Feed。

## 30.13 Diversity

- repeated author blocked in window。
- window slides。
- relaxation on shortage。
- near duplicate penalty。
- non-duplicate no penalty。
- missing embedding no penalty。
- deterministic ties。
- no full similarity matrix requirement。

## 30.14 Final selection

- fresh first。
- soft second。
- all source mix。
- final len reaches limit when enough。
- no duplicate IDs。
- FinalScore exact。
- response score equals FinalScore。

## 30.15 Trace

- one request saved。
- N results -> N rows。
- positions exact。
- flags exact。
- network/novel/soft exact。
- components exact。
- BaseScore component sum。
- FinalScore = Base - penalty。
- request/result transaction rollback together。
- persistence failure does not change HTTP 200。

## 30.16 Cleanup

- expired results deleted。
- active results retained。
- old request deleted。
- cascade works。
- batch bound。
- context cancellation。
- error metered and loop survives。

## 30.17 API compatibility

- response JSON unchanged。
- tracking claims verify。
- token version remains v2。
- ranker version rules_v2。
- config hash consistent between response and DB。

---

# 31. Execution phases

严格顺序：

    Phase 0:
    preflight and baseline

    Phase 1:
    config, versions, config hash

    Phase 2:
    reply behavior

    Phase 3:
    canonical multi-signal capped outcome

    Phase 4:
    positive/negative profiles and confidence

    Phase 5:
    result trace schema and served-history loader

    Phase 6:
    unified eligibility and four recall sources

    Phase 7:
    merge, batch hydration, negative similarity

    Phase 8:
    author affinity and follow set

    Phase 9:
    score breakdown and rank formula

    Phase 10:
    network balance, discovery, diversity

    Phase 11:
    trace persistence, retention cleanup, metrics

    Phase 12:
    remove v1 runtime

    Phase 13:
    migration and full verification

每 Phase：

    gofmt changed Go files
    run affected package tests
    inspect git diff

测试失败先修复，不得继续堆后续阶段。

---

# 32. Verification

## 32.1 Static and unit

在 Go.exchange：

    gofmt -w changed-go-files
    gofmt -l changed-go-files
    go vet ./...
    go test ./... -count=1
    git diff --check

gofmt -l 必须无本任务文件输出。

## 32.2 Disposable PostgreSQL 16

Feed v2 的 PostgreSQL 验收必须使用 disposable PostgreSQL 16 + pgvector。

不得：

- 使用 SQLite 代替。
- 使用 mock 代替 SQL 行为。
- 使用现有用户数据库。
- 使用假 DSN。
- 因 POSTGRES_TEST_DSN 缺失就宣称通过。

推荐流程：

1. 启动临时 pgvector/pgvector:pg16 container。
2. 使用随机 host port。
3. bounded polling 等待 pg_isready。
4. 设置 POSTGRES_TEST_DSN。
5. 运行 Feed v2 相关集成测试。
6. 设置 DATABASE_DSN 指向同一临时数据库。
7. 执行 migration 两次。
8. 重新运行相关 SQL 测试。
9. 删除临时 container。

必须报告实际 DSN 类型和 PostgreSQL major version，但不得泄露密码。

## 32.3 Migration

在 disposable DB：

    go run ./cmd/migrate
    go run ./cmd/migrate

两次都必须 exit 0。

验证表、constraints、indexes 和 FK。

## 32.4 Frontend

从 Exchangeapp_frontend：

    npm.cmd ci
    npm.cmd test
    npm.cmd exec vue-tsc -- --noEmit
    npm.cmd exec vite -- build

Windows 使用 npm.cmd，不使用 npm.ps1。

如果 vue-tsc 生成 src 下 JavaScript，必须先确认是本次生成的未跟踪 artifact，再只删除这些 artifact。

## 32.5 Compose

从 monorepo root：

    docker compose config --quiet

本任务不得破坏：

    db
    embedding
    redis
    kafka
    migrate
    api
    worker

Compose config 成功只证明配置展开，不证明容器运行健康。

## 32.6 Optional browser acceptance

如果浏览器环境可用，验证：

- Following 只有关注作者并按发布时间排序。
- For You 同时出现网络内外内容。
- 刷新不立即重复 hard-served 内容。
- NI 内容消失。
- 新 Like/Reply 能在之后请求恢复资格。

浏览器不可用时报告 NOT VERIFIED，不得写 PASS。

## 32.7 Honest status

每项只允许：

    PASS
    FAIL
    SKIPPED
    BLOCKED
    NOT VERIFIED

PostgreSQL、Redis、Docker、race、browser 不能由 unit/build 结果推断。

---

# 33. Performance and failure semantics

## 33.1 No N+1

禁止逐 candidate 查询：

- Article。
- Author。
- Embedding。
- Follow state。
- Author affinity history。
- Served history。

数据库 query 数量必须与 candidate 数量 O(1)。

## 33.2 Bounded work

    candidates <= 500
    results <= 50
    served history <= 1000
    cleanup batch <= configured limit

Trace batch insert。

Selection 不构建 500 x 500 matrix。

## 33.3 Fatal vs nonfatal

返回 500：

- core behavior/profile SQL 失败。
- core candidate retrieval SQL 失败。
- article hydration 失败。
- rank 必需 author/follow 数据失败。

非 fatal：

- served history load 失败。
- request/result trace persist 失败。
- cleanup 失败。
- 单篇 candidate embedding 缺失或非法。

非 fatal 必须 log + metric。

---

# 34. Definition of Done

必须全部满足：

- Following API 保持独立时间线。
- For You 有四源召回。
- Reply 与 comment creation 原子。
- Reply 删除后保留历史。
- Like + Reply 事实共存但单文章 contribution capped。
- PositiveVector 存在。
- NegativeVector 存在。
- NegativeConfidence 存在。
- negative 不直接进入 PositiveVector。
- full-history NI precedence 正确。
- read_end_recency_v2 保持。
- semantic recall 只用 PositiveVector。
- cold start 可收到 followed-author 内容。
- negative semantic penalty 带 confidence。
- author affinity 与 follow bonus 存在。
- Recent/Popular 使用 PublishedAt。
- popularity 使用 like + comment。
- self-authored 不进入 For You。
- hard/soft served 行为正确。
- network/out-of-network 分类使用实际 follow set。
- novel discovery 不是 Recent/Popular 别名。
- author diversity 存在。
- semantic duplicate penalty 存在。
- selection deterministic。
- RecommendationResultTrace 存在。
- trace retention cleanup 存在。
- source attribution 与 score breakdown 存在。
- no N+1。
- no embedding_v1 runtime fallback。
- API response schema 不变。
- tracking token version 仍为 v2。
- migration x2 PASS。
- PostgreSQL 16 Feed v2 integration PASS。
- go vet PASS。
- go test PASS。
- frontend test/typecheck/build PASS。
- docker compose config PASS。
- 所有未执行验收诚实标记。

---

# 35. Final report format

完成后必须输出：

## Baseline

- Git root。
- branch。
- starting HEAD。
- dirty-worktree handling。

## Changed

- 主要新增/修改文件。

## Removed

- 删除的 v1 helpers 和 obsolete runtime。

## Behavior Changes

说明：

- Reply。
- canonical outcome。
- contribution cap。
- positive/negative profiles。
- four-source recall。
- served exclusion。
- author affinity。
- network balance。
- discovery。
- diversity。

## Database

列出：

- RecommendationRequest changes。
- RecommendationResultTrace。
- constraints/indexes。
- retention cleanup。

## API Compatibility

确认 response、tracking token 和 Following API。

## Tests

逐项报告：

    gofmt
    go vet
    go test
    PostgreSQL 16 integration
    migration x2
    frontend test
    frontend typecheck
    frontend build
    docker compose config
    browser acceptance

## Failures

任何命令未执行或失败必须明确写出，不得包装成通过。

## Not Implemented

明确列出：

- ML ranker。
- HNSW/IVFFlat。
- persistent user vector。
- multi-interest vector。
- repost/quote。
- 2-hop graph。
- block/mute。
- Article to Post rename。

---

# 36. Final execution rule

本规范交给实现 Agent 后，Agent 应直接按 Phase 0 到 Phase 13 执行。

Agent 不得：

- 重新讨论已冻结架构。
- 将本文拆成 v1/v2 双运行路径。
- 为赶进度跳过失败测试。
- 使用 mock 或 SQLite 代替要求的 PostgreSQL 验收。
- 把 SKIPPED、BLOCKED 或 NOT VERIFIED 写成 PASS。
- 在未授权情况下 commit 或 push。

如果外部环境阻止 Docker、PostgreSQL、前端依赖或浏览器验证，Agent 继续完成所有不依赖该环境的工作，然后在最终报告中准确标记阻塞项。
