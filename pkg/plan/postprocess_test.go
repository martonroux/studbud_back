package plan

import (
	"reflect"
	"testing"
	"time"
)

func mustDay(s string) time.Time {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNormalizeDays_DropsHallucinatedIDs(t *testing.T) {
	in := PostProcessInput{
		Days: []Day{
			{Date: "2026-05-02", PrimarySubjectCards: []int64{1, 999}, CrossSubjectCards: []int64{205}},
		},
		PrimaryIDs: []int64{1, 2, 3},
		CrossIDs:   []int64{205},
	}
	out := NormalizeDays(in, mustDay("2026-05-01"), mustDay("2026-05-02"))
	if len(out) != 2 {
		t.Fatalf("len(days) = %d, want 2", len(out))
	}
	day1 := out[1]
	if !reflect.DeepEqual(day1.PrimarySubjectCards, []int64{1}) {
		t.Errorf("primary = %v, want [1]", day1.PrimarySubjectCards)
	}
	if !reflect.DeepEqual(day1.CrossSubjectCards, []int64{205}) {
		t.Errorf("cross = %v, want [205]", day1.CrossSubjectCards)
	}
}

func TestNormalizeDays_DropsDuplicatesAcrossDays(t *testing.T) {
	in := PostProcessInput{
		Days: []Day{
			{Date: "2026-05-01", PrimarySubjectCards: []int64{1, 2}},
			{Date: "2026-05-02", PrimarySubjectCards: []int64{2, 3}},
		},
		PrimaryIDs: []int64{1, 2, 3},
	}
	out := NormalizeDays(in, mustDay("2026-05-01"), mustDay("2026-05-02"))
	if !reflect.DeepEqual(out[0].PrimarySubjectCards, []int64{1, 2}) {
		t.Errorf("day0 primary = %v, want [1 2]", out[0].PrimarySubjectCards)
	}
	if !reflect.DeepEqual(out[1].PrimarySubjectCards, []int64{3}) {
		t.Errorf("day1 primary = %v, want [3]", out[1].PrimarySubjectCards)
	}
}

func TestNormalizeDays_ClampsRange(t *testing.T) {
	in := PostProcessInput{
		Days: []Day{
			{Date: "2026-04-30", PrimarySubjectCards: []int64{1}}, // before today
			{Date: "2026-05-03", PrimarySubjectCards: []int64{2}}, // after exam
			{Date: "2026-05-01", PrimarySubjectCards: []int64{3}}, // in range
		},
		PrimaryIDs: []int64{1, 2, 3},
	}
	out := NormalizeDays(in, mustDay("2026-05-01"), mustDay("2026-05-02"))
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2 (today + exam)", len(out))
	}
	if !reflect.DeepEqual(out[0].PrimarySubjectCards, []int64{3}) {
		t.Errorf("day0 = %v, want [3]", out[0].PrimarySubjectCards)
	}
	if len(out[1].PrimarySubjectCards) != 0 {
		t.Errorf("day1 should be empty bucket, got %v", out[1].PrimarySubjectCards)
	}
}

func TestNormalizeDays_FillsMissingDays(t *testing.T) {
	in := PostProcessInput{
		Days:       []Day{{Date: "2026-05-01", PrimarySubjectCards: []int64{1}}},
		PrimaryIDs: []int64{1},
	}
	out := NormalizeDays(in, mustDay("2026-05-01"), mustDay("2026-05-04"))
	if len(out) != 4 {
		t.Fatalf("len = %d, want 4 days", len(out))
	}
	for i, want := range []string{"2026-05-01", "2026-05-02", "2026-05-03", "2026-05-04"} {
		if out[i].Date != want {
			t.Errorf("day[%d].Date = %q, want %q", i, out[i].Date, want)
		}
	}
}

func TestCalculateDrift_NoPastDays_ZeroBehind(t *testing.T) {
	days := []Day{{Date: "2026-05-02", PrimarySubjectCards: []int64{1, 2}}}
	d := CalculateDrift(days, map[string]map[int64]bool{}, mustDay("2026-05-01"))
	if d.DaysBehind != 0 || d.ShouldSuggestRegen {
		t.Fatalf("drift = %+v, want zero", d)
	}
}

func TestCalculateDrift_FullCompletion_ZeroBehind(t *testing.T) {
	days := []Day{
		{Date: "2026-04-29", PrimarySubjectCards: []int64{1, 2}},
		{Date: "2026-04-30", PrimarySubjectCards: []int64{3}},
	}
	progress := map[string]map[int64]bool{
		"2026-04-29": {1: true, 2: true},
		"2026-04-30": {3: true},
	}
	d := CalculateDrift(days, progress, mustDay("2026-05-01"))
	if d.DaysBehind != 0 {
		t.Fatalf("DaysBehind = %d, want 0", d.DaysBehind)
	}
}

func TestCalculateDrift_ThreeBehind_SuggestsRegen(t *testing.T) {
	days := []Day{
		{Date: "2026-04-28", PrimarySubjectCards: []int64{1, 2, 3, 4}}, // 0/4
		{Date: "2026-04-29", PrimarySubjectCards: []int64{5, 6}},       // 0/2
		{Date: "2026-04-30", PrimarySubjectCards: []int64{7}},          // 0/1
	}
	d := CalculateDrift(days, map[string]map[int64]bool{}, mustDay("2026-05-01"))
	if d.DaysBehind != 3 {
		t.Errorf("DaysBehind = %d, want 3", d.DaysBehind)
	}
	if !d.ShouldSuggestRegen {
		t.Error("ShouldSuggestRegen = false, want true")
	}
}

func TestCalculateDrift_EmptyDay_NotBehind(t *testing.T) {
	days := []Day{{Date: "2026-04-30"}}
	d := CalculateDrift(days, map[string]map[int64]bool{}, mustDay("2026-05-01"))
	if d.DaysBehind != 0 {
		t.Errorf("DaysBehind = %d, want 0 (empty day not behind)", d.DaysBehind)
	}
}
