package keywordWorker

import "testing"

func TestMaterialChange_Identity(t *testing.T) {
	if MaterialChange("q", "a", "q", "a") {
		t.Error("identity should not be material")
	}
}

func TestMaterialChange_TrailingWhitespace(t *testing.T) {
	if MaterialChange("hello", "world", "hello ", "world") {
		t.Error("trailing whitespace should not be material")
	}
}

func TestMaterialChange_TwentyCharAddition(t *testing.T) {
	old := "Quelle est la phase ?"
	newQ := "Quelle est la phase de la mitose dans le cycle cellulaire ?"
	if !MaterialChange(old, "answer", newQ, "answer") {
		t.Error("20+ char addition should be material")
	}
}

func TestMaterialChange_FullRewrite(t *testing.T) {
	if !MaterialChange("foo", "bar", "completely different content here please", "ok") {
		t.Error("full rewrite should be material")
	}
}

func TestMaterialChange_EmptyToEmpty(t *testing.T) {
	if MaterialChange("", "", "", "") {
		t.Error("empty to empty should not be material")
	}
}

func TestMaterialChange_TypoFix(t *testing.T) {
	if MaterialChange("Quelle est la phase ?", "Phase A", "Quelle est la phase ?", "Phase A.") {
		t.Error("trailing period should not be material")
	}
}
