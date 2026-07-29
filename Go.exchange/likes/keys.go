package likes

import "fmt"

const (
	DirtyKey      = "article:likes:dirty"
	ProcessingKey = "article:likes:processing"
	ClaimsKey     = "article:likes:claims"

	BehaviorDirtyKey      = "article:likes:behavior:dirty"
	BehaviorStateKey      = "article:likes:behavior:state"
	BehaviorProcessingKey = "article:likes:behavior:processing"
	BehaviorClaimsKey     = "article:likes:behavior:claims"
)

func ReadyKey(articleID uint) string             { return fmt.Sprintf("article:like:%d:ready", articleID) }
func CountKey(articleID uint) string             { return fmt.Sprintf("article:like:%d:count", articleID) }
func UsersKey(articleID uint) string             { return fmt.Sprintf("article:like:%d:users", articleID) }
func VersionKey(articleID uint) string           { return fmt.Sprintf("article:like:%d:version", articleID) }
func BehaviorPair(userID, articleID uint) string { return fmt.Sprintf("%d:%d", userID, articleID) }
