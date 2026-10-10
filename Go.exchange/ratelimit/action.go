package ratelimit

// Action identifies an application operation protected by a rate-limit policy.
type Action string

const (
	ActionPostCreate      Action = "post_create"
	ActionPostSearch      Action = "post_search"
	ActionFollowMutation  Action = "follow_mutation"
	ActionMediaUpload     Action = "media_upload"
	ActionTranslation     Action = "translation"
	ActionRecommendations Action = "recommendations"
	ActionLikeMutation    Action = "like_mutation"
)
