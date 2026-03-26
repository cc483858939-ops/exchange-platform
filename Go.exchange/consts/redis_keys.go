package consts

import "time"

const (

	// fmt.Sprintf(ArticleLikeKey, articleID)
	ArticleLikeKey = "article:%s:likes"

	//
	ArticleUserSet = "article:%s:users"

	// 待同步的脏数据集合 (Set)
	ArticleDirtySetKey = "article:likes:dirty_set"

	//用来接收待处理的集合
	ArticleProcessingSetKey = "article:likes:processing_set"
	// 过期时间配置 (24小时)
	ArticleLikeExpire = 24 * time.Hour
	//配置lua脚本减少网络io保证原子性移动以免MySQL覆盖
	//一共两个set我们需要做的就是看脏集合里面的ID如果处理集合里面没有，那我们就直接SMOVE的操作把对应的id移动到另一个集合里面。如果处理集合里面本来就有的话我们就不移过去
	// 记录每个 article ID 的落库失败次数 (Hash: articleID -> retryCount)
	ArticleLikeRetryCountKey = "article:likes:retry_counts"
	// 超过最大重试次数后的死信集合 (Set)
	ArticleLikeDeadLetterKey = "article:likes:dead_letter"
	// 最大重试次数，超过后进入死信不再重试
	MaxRetryCount = 3

	Refresh = "refresh_token:%s" //用来实现双token
	FetchSafeBatchScript = `
	local dirty_set = KEYS[1]
	local processing_set = KEYS[2]
	local batch_size = ARGV[1]
	local key_pattern = ARGV[2]
   -- 两个set我还要一个数去接收一次srand的数 我还要一个string暂时的接收id
	-- 1. 随机看一批，不从 dirty 删除，防止处理一半挂了数据丢失
	local candidates = redis.call("SRANDMEMBER", dirty_set, batch_size)
	if #candidates == 0 then return {} end

	local result = {}
	
	for _, id in ipairs(candidates) do
		-- 2. 互斥检查 如果 processing_set 里有，说明别的协程正在处理，跳过
		-- 这样保证了同一时刻，一个 ID 只有一个任务在跑，解决了 MySQL 覆盖新值的问题
		if redis.call("SISMEMBER", processing_set, id) == 0 then
			
			-- 3. 原子移动：从 dirty -> processing (相当于抢锁)
			local moved = redis.call("SMOVE", dirty_set, processing_set, id)
			if moved == 1 then
				local like_key = string.format(key_pattern, id)
				local val = redis.call("GET", like_key)
				if val then
					table.insert(result, id)
					table.insert(result, val)
				else
					-- 异常保护：移动成功但没取到值  移除锁防止死锁
					redis.call("SREM", processing_set, id)
				end
			end
		end
	end
	return result
	`
)
