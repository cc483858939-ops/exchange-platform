package tasks

import (
	"strconv"

	"Go.exchange/likes"

	"github.com/go-redis/redis/v7"
)

func resetLikeRelayIntegrationQueues(client *redis.Client) error {
	if client == nil {
		return nil
	}
	return client.Del(
		likes.DirtyKey,
		likes.ProcessingKey,
		likes.ClaimsKey,
		likes.BehaviorDirtyKey,
		likes.BehaviorStateKey,
		likes.BehaviorProcessingKey,
		likes.BehaviorClaimsKey,
	).Err()
}

func cleanupLikeRelayIntegrationState(client *redis.Client, postIDs, userIDs []uint) error {
	if client == nil {
		return nil
	}
	if len(postIDs) == 0 {
		return nil
	}
	pipe := client.Pipeline()
	for _, postID := range postIDs {
		if postID == 0 {
			continue
		}
		postIDString := strconv.FormatUint(uint64(postID), 10)
		pipe.Del(likes.ReadyKey(postID), likes.CountKey(postID), likes.UsersKey(postID), likes.VersionKey(postID))
		pipe.SRem(likes.DirtyKey, postID)
		pipe.ZRem(likes.ProcessingKey, postIDString)
		pipe.HDel(likes.ClaimsKey, postIDString)
		pipe.SRem(likes.RegistryKey, postID)
		pipe.ZRem(likes.ExpiryCandidatesKey, postIDString)
		pipe.HDel(likes.RecoverableVersionsKey, postIDString)
		for _, userID := range userIDs {
			if userID == 0 {
				continue
			}
			pair := likes.BehaviorPair(userID, postID)
			pipe.SRem(likes.BehaviorDirtyKey, pair)
			pipe.HDel(likes.BehaviorStateKey, pair)
			pipe.ZRem(likes.BehaviorProcessingKey, pair)
			pipe.HDel(likes.BehaviorClaimsKey, pair)
		}
	}
	_, err := pipe.Exec()
	return err
}
