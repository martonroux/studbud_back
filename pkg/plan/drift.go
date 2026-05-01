package plan

import "time"

// driftThreshold is the minimum number of behind-days that triggers a regen suggestion.
const driftThreshold = 2

// behindRatio is the completion ratio at or below which a past day counts as "behind".
const behindRatio = 0.5

// CalculateDrift counts past days where the user fell below `behindRatio` completion.
// `progressByDate` maps "YYYY-MM-DD" to the set of FC IDs the user marked done that day.
// Today and future days are ignored.
func CalculateDrift(days []Day, progressByDate map[string]map[int64]bool, today time.Time) Drift {
	cutoff := startOfDay(today)
	behind := 0
	for _, d := range days {
		parsed, err := time.Parse(dateLayout, d.Date)
		if err != nil {
			continue
		}
		if !parsed.Before(cutoff) {
			continue
		}
		if dayIsBehind(d, progressByDate[d.Date]) {
			behind++
		}
	}
	return Drift{
		DaysBehind:         behind,
		ShouldSuggestRegen: behind >= driftThreshold,
	}
}

// dayIsBehind returns true when fewer than `behindRatio` of the day's primary+cross
// cards were marked done. Days with zero assigned cards are never behind.
func dayIsBehind(d Day, doneIDs map[int64]bool) bool {
	assigned := append([]int64(nil), d.PrimarySubjectCards...)
	assigned = append(assigned, d.CrossSubjectCards...)
	if len(assigned) == 0 {
		return false
	}
	done := 0
	for _, id := range assigned {
		if doneIDs[id] {
			done++
		}
	}
	ratio := float64(done) / float64(len(assigned))
	return ratio < behindRatio
}
