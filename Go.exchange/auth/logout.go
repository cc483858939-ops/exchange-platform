package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-redis/redis/v7"
)

// A captured secret may have rotated while logout waited for the client lock.
// A used-secret marker proves membership of this family, without accepting an
// arbitrary sid or permitting a recovery record to resurrect a deleted family.
// Used/recovery records keep their existing TTLs; no key scan is needed.
var revokeRefreshSessionScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then
  return 1
end
if redis.call('HGET', KEYS[1], 'secret_hash') ~= ARGV[1]
   and redis.call('EXISTS', KEYS[2]) == 0 then
  return -1
end
redis.call('DEL', KEYS[1])
return 1
`)

func (s *RedisRefreshStore) Revoke(ctx context.Context, sessionID, expectedSecretHash string) error {
	if ctx == nil || sessionID == "" || expectedSecretHash == "" {
		return errors.New("invalid refresh revocation parameters")
	}
	result, err := revokeRefreshSessionScript.Run(s.client.WithContext(ctx),
		[]string{refreshSessionKey(sessionID), usedRefreshKey(sessionID, expectedSecretHash)}, expectedSecretHash).Int64()
	if err != nil {
		return fmt.Errorf("revoke refresh session: %w", err)
	}
	switch result {
	case 1:
		return nil
	case -1:
		return ErrRefreshInvalid
	default:
		return errors.New("invalid Redis refresh revocation response")
	}
}
