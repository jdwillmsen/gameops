package telemetry

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFinishedCarriesTheUnitsReasonCode(t *testing.T) {
	var recent []recentTask
	raw := `[{"id":"a","state":"failed","reason":"no axe in inventory","reason_code":"missing_tool"},
	          {"id":"b","state":"failed","reason":"no axe in inventory"}]`
	if err := json.Unmarshal([]byte(raw), &recent); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if ev := finished("a", "mine", recent, now); ev.ReasonCode != "missing_tool" || ev.Reason != "no axe in inventory" {
		t.Errorf("%+v", ev)
	}
	if ev := finished("b", "mine", recent, now); ev.ReasonCode != "" {
		t.Errorf("a unit without codes produced %q", ev.ReasonCode)
	}
}
