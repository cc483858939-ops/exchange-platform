package likes

import "fmt"

const (
	DirtyKey               = "post:likes:dirty"
	ProcessingKey          = "post:likes:processing"
	ClaimsKey              = "post:likes:claims"
	RegistryKey            = "post:likes:registry"
	ExpiryCandidatesKey    = "post:likes:expiry:candidates"
	RecoverableVersionsKey = "post:likes:recoverable:versions"

	BehaviorDirtyKey      = "post:likes:behavior:dirty"
	BehaviorStateKey      = "post:likes:behavior:state"
	BehaviorProcessingKey = "post:likes:behavior:processing"
	BehaviorClaimsKey     = "post:likes:behavior:claims"

	UserLikesInitSentinel = "0"
)

func ReadyKey(postID uint) string { return fmt.Sprintf("post:like:%d:ready", postID) }
func CountKey(postID uint) string { return fmt.Sprintf("post:like:%d:count", postID) }

// UsersKey names the removed legacy Post -> Users key. Production Like paths
// must not read or write it; it remains available for cleanup and assertions.
func UsersKey(postID uint) string             { return fmt.Sprintf("post:like:%d:users", postID) }
func VersionKey(postID uint) string           { return fmt.Sprintf("post:like:%d:version", postID) }
func RebuildTokenKey(postID uint) string      { return fmt.Sprintf("post:like:%d:rebuild-token", postID) }
func UserLikesKey(userID uint) string         { return fmt.Sprintf("user:likes:%d", userID) }
func BehaviorPair(userID, postID uint) string { return fmt.Sprintf("%d:%d", userID, postID) }
