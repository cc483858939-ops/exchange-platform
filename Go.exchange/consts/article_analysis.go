package consts

const (
	// ArticleAnalysisDirtySetKey 存放待 AI 分析的文章 ID。
	ArticleAnalysisDirtySetKey = "article:analysis:dirty_set"
	// ArticleAnalysisProcessingSetKey 存放已经被 worker 抢到、正在处理的文章 ID。
	ArticleAnalysisProcessingSetKey = "article:analysis:processing_set"

	// 文章 AI 分析状态只由服务端维护，不允许客户端直接写。
	ArticleStatusPending = "pending"
	// pending 表示文章已创建，等待后台分析。
	ArticleStatusProcessing = "processing"
	// completed 表示 AI 结果已经成功回写数据库。
	ArticleStatusCompleted = "completed"
	// failed 表示本次异步分析失败，需要人工排查或后续补偿。
	ArticleStatusFailed = "failed"

	// FetchArticleAnalysisBatchScript 的作用是：
	// 1. 从待处理集合里随机取一批文章 ID。
	// 2. 只有不在 processing_set 里的 ID 才允许被抢占。
	// 3. 抢占成功后把 ID 从 dirty_set 原子移动到 processing_set。
	// 这样可以保证同一篇文章同一时刻只会被一个 worker 处理。
	FetchArticleAnalysisBatchScript = `
	local dirty_set = KEYS[1]
	local processing_set = KEYS[2]
	local batch_size = tonumber(ARGV[1])
	local candidates = redis.call("SRANDMEMBER", dirty_set, batch_size)
	if #candidates == 0 then return {} end

	local result = {}
	for _, id in ipairs(candidates) do
		if redis.call("SISMEMBER", processing_set, id) == 0 then
			local moved = redis.call("SMOVE", dirty_set, processing_set, id)
			if moved == 1 then
				table.insert(result, id)
			end
		end
	end
	return result
	`
)
