package consts

import "time"

const (

	// Use: fmt.Sprintf(ArticleLikeKey, articleID)
	ArticleLikeKey = "article:%s:likes"

	// 暂时没用到，留着给以后做“去重”用
	ArticleUserSet = "article:%s:users"

	// 待同步的脏数据集合 (Set)
	ArticleDirtySetKey = "article:likes:dirty_set"

	// 过期时间配置 (24小时)
	ArticleLikeExpire = 24 * time.Hour
)
