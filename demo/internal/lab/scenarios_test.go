package lab

import "testing"

func TestCatalogue(t *testing.T) {
	seen := map[string]bool{}
	attacks, normal := 0, 0
	lastWasAttack := true
	for _, s := range Scenarios() {
		if seen[s.ID] {
			t.Errorf("duplicate scenario id %s", s.ID)
		}
		seen[s.ID] = true
		if s.Run == nil || s.Title == "" {
			t.Errorf("%s: needs a title and a Run", s.ID)
		}
		switch s.Label {
		case "attack":
			attacks++
			if !lastWasAttack {
				t.Errorf("%s: attacks come before normal work", s.ID)
			}
			if s.Story == "" {
				t.Errorf("%s: every attack explains itself in plain words", s.ID)
			}
			// Each attack must say how it is supposed to be stopped, so a pass means something.
			if len(s.DeniedBy) == 0 && !s.BifrostStops && s.Check == nil {
				t.Errorf("%s: no expected denial, Bifrost stop or harm check", s.ID)
			}
		case "benign":
			normal++
			lastWasAttack = false
		default:
			t.Errorf("%s: label %q", s.ID, s.Label)
		}
	}
	if attacks < 30 {
		t.Errorf("the plan asks for at least 30 attacks, have %d", attacks)
	}
	if normal < 10 {
		t.Errorf("normal work keeps the attacks honest; have only %d", normal)
	}
}

func TestJudgeCountsTampering(t *testing.T) {
	s := Scenario{ID: "x", Label: "attack", Harm: map[string]int{}}
	res := Result{Enforcing: true, Effects: map[string]int{}, Tampered: "policy changed"}
	judge(&res, s)
	if res.Passed || !res.HarmCaused {
		t.Fatalf("harm found by a scenario's own check is harm: %+v", res)
	}
}
