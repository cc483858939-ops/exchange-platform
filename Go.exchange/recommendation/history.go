package recommendation

import "time"

func ClassifyServedAt(postID uint, lastServedAt time.Time, window HistoryWindow) (ServedItem, bool) {
	lastServedAt = lastServedAt.UTC()
	if !window.HardStart.IsZero() && !lastServedAt.Before(window.HardStart) {
		return ServedItem{PostID: postID, LastServedAt: lastServedAt, Hard: true}, true
	}
	if !window.SoftStart.IsZero() && !lastServedAt.Before(window.SoftStart) {
		return ServedItem{PostID: postID, LastServedAt: lastServedAt, Soft: true}, true
	}
	return ServedItem{}, false
}
