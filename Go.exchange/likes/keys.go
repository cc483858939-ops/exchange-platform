package likes

import "fmt"

const (
	DirtyKey      = "post:likes:dirty"
	ProcessingKey = "post:likes:processing"
	ClaimsKey     = "post:likes:claims"

	BehaviorDirtyKey      = "post:likes:behavior:dirty"
	BehaviorStateKey      = "post:likes:behavior:state"
	BehaviorProcessingKey = "post:likes:behavior:processing"
	BehaviorClaimsKey     = "post:likes:behavior:claims"
)

func ReadyKey(postID uint) string             { return fmt.Sprintf("post:like:%d:ready", postID) }
func CountKey(postID uint) string             { return fmt.Sprintf("post:like:%d:count", postID) }
func UsersKey(postID uint) string             { return fmt.Sprintf("post:like:%d:users", postID) }
func VersionKey(postID uint) string           { return fmt.Sprintf("post:like:%d:version", postID) }
func BehaviorPair(userID, postID uint) string { return fmt.Sprintf("%d:%d", userID, postID) }
