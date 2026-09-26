package recommendation

import (
	"sort"
	"strings"
)

func normalizeDirtyUsers(userIDs []uint) []uint {
	seen := make(map[uint]struct{}, len(userIDs))
	ids := make([]uint, 0, len(userIDs))
	for _, userID := range userIDs {
		if userID == 0 {
			continue
		}
		if _, exists := seen[userID]; exists {
			continue
		}
		seen[userID] = struct{}{}
		ids = append(ids, userID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func normalizeDirtyReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) > 64 {
		return reason[:64]
	}
	return reason
}
