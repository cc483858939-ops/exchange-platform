package devdata

import "sort"

// DesiredStateDiff is calculated by immutable source Post ID. It is kept
// independent from GORM so the state transition rules can be tested without a
// database or a live X account.
type DesiredStateDiff struct {
	Keep       []string
	Reactivate []string
	Insert     []string
	Retire     []string
}

func CalculateDesiredStateDiff(active, tombstone, desired []string) DesiredStateDiff {
	activeSet := make(map[string]struct{}, len(active))
	tombstoneSet := make(map[string]struct{}, len(tombstone))
	desiredSet := make(map[string]struct{}, len(desired))
	for _, id := range active {
		if id != "" {
			activeSet[id] = struct{}{}
		}
	}
	for _, id := range tombstone {
		if id != "" {
			tombstoneSet[id] = struct{}{}
		}
	}
	for _, id := range desired {
		if id != "" {
			desiredSet[id] = struct{}{}
		}
	}

	diff := DesiredStateDiff{}
	for id := range desiredSet {
		if _, exists := activeSet[id]; exists {
			diff.Keep = append(diff.Keep, id)
			continue
		}
		if _, exists := tombstoneSet[id]; exists {
			diff.Reactivate = append(diff.Reactivate, id)
			continue
		}
		diff.Insert = append(diff.Insert, id)
	}
	for id := range activeSet {
		if _, exists := desiredSet[id]; !exists {
			diff.Retire = append(diff.Retire, id)
		}
	}
	sort.Strings(diff.Keep)
	sort.Strings(diff.Reactivate)
	sort.Strings(diff.Insert)
	sort.Strings(diff.Retire)
	return diff
}
