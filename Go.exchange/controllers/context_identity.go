package controllers

import "strconv"

func jwtUserIDClaim(value interface{}) (uint, bool) {
	switch userID := value.(type) {
	case uint:
		return userID, userID > 0
	case uint64:
		converted := uint(userID)
		return converted, converted > 0 && uint64(converted) == userID
	case int:
		return uint(userID), userID > 0
	case int64:
		converted := uint(userID)
		return converted, userID > 0 && int64(converted) == userID
	case float64:
		converted := uint(userID)
		return converted, converted > 0 && userID == float64(converted)
	case string:
		parsed, err := strconv.ParseUint(userID, 10, 64)
		converted := uint(parsed)
		return converted, err == nil && converted > 0 && uint64(converted) == parsed
	default:
		return 0, false
	}
}
