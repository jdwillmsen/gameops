package work

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func bulkBody(units, confirm, rest string) string {
	return `{"units":` + units + `,"confirm":` + confirm + `,` + rest + `}`
}

const recallTo = `"action":"recall","to":{"x":10,"y":64,"z":-20}`

func TestBulkRecallStopsThenQueuesGotoPerUnit(t *testing.T) {
	o := newOpRig(t, NewMemory())
	if code, _ := o.post(t, "/v1/work/crew-01/tasks", gatherOK, nil); code != 202 {
		t.Fatal(code)
	}
	code, body := o.post(t, "/v1/work/bulk", bulkBody(`["crew-01","crew-02"]`, "2", recallTo), nil)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var out struct {
		Results []struct {
			Unit string `json:"unit"`
			OK   bool   `json:"ok"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || len(out.Results) != 2 || !out.Results[0].OK || !out.Results[1].OK {
		t.Fatalf("results: %s", body)
	}
	items, _ := o.store.Recent(context.Background(), "crew-01", 10)
	var gotos, stops, cancelled int
	for _, it := range items {
		switch {
		case it.Kind == KindCommand:
			stops++
		case strings.Contains(string(it.Body), `"goto"`):
			gotos++
		case it.State == Cancelled:
			cancelled++
		}
	}
	if gotos != 1 || stops != 1 || cancelled != 1 {
		t.Fatalf("gotos=%d stops=%d cancelled=%d", gotos, stops, cancelled)
	}
	if got := o.notified(); len(got) < 2 {
		t.Fatalf("units not woken: %v", got)
	}
}

func TestBulkAssignQueuesTheTaskWithoutStopping(t *testing.T) {
	o := newOpRig(t, NewMemory())
	code, body := o.post(t, "/v1/work/bulk", bulkBody(`["crew-01","crew-02"]`, "2", `"action":"assign","task":`+gatherOK), nil)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	for _, u := range []string{"crew-01", "crew-02"} {
		items, _ := o.store.Recent(context.Background(), u, 10)
		if len(items) != 1 || items[0].Kind != KindTask || items[0].Operator != "owner" {
			t.Fatalf("%s: %+v", u, items)
		}
	}
}

func TestBulkRefusesBeforeQueuingAnything(t *testing.T) {
	big := make([]string, maxBulk+1)
	for i := range big {
		big[i] = `"crew-01"`
	}
	cases := map[string]string{
		"unconfirmed":    bulkBody(`["crew-01","crew-02"]`, "1", recallTo),
		"no confirm":     `{"units":["crew-01"],` + recallTo + `}`,
		"unknown unit":   bulkBody(`["crew-01","nope"]`, "2", recallTo),
		"duplicate":      bulkBody(`["crew-01","crew-01"]`, "2", recallTo),
		"empty":          bulkBody(`[]`, "0", recallTo),
		"too many":       bulkBody("["+strings.Join(big, ",")+"]", "17", recallTo),
		"bad action":     bulkBody(`["crew-01"]`, "1", `"action":"explode"`),
		"recall no dest": bulkBody(`["crew-01"]`, "1", `"action":"recall"`),
		"recall outside": bulkBody(`["crew-01"]`, "1", `"action":"recall","to":{"x":9999,"y":64,"z":0}`),
		"assign bad":     bulkBody(`["crew-01"]`, "1", `"action":"assign","task":{"template":"nope"}`),
		"unknown field":  bulkBody(`["crew-01"]`, "1", recallTo+`,"extra":1`),
	}
	for name, body := range cases {
		o := newOpRig(t, NewMemory())
		code, resp := o.post(t, "/v1/work/bulk", body, nil)
		if code != 400 && code != 404 {
			t.Errorf("%s: %d %s", name, code, resp)
		}
		for _, u := range []string{"crew-01", "crew-02"} {
			if got, _ := o.store.Recent(context.Background(), u, 10); len(got) != 0 {
				t.Errorf("%s queued %d items for %s", name, len(got), u)
			}
		}
	}
}

func TestBulkNeedsSessionAndHeader(t *testing.T) {
	o := newOpRig(t, NewMemory())
	b := bulkBody(`["crew-01"]`, "1", recallTo)
	if code, _ := o.post(t, "/v1/work/bulk", b, map[string]string{"X-Test-Op": ""}); code != 401 {
		t.Errorf("anonymous: %d", code)
	}
	if code, _ := o.post(t, "/v1/work/bulk", b, map[string]string{"X-Crew-Request": ""}); code != 403 {
		t.Errorf("no header: %d", code)
	}
}

func TestBulkReportsAPerUnitFailureWhenTheStoreIsDown(t *testing.T) {
	o := newOpRig(t, downStore{})
	code, body := o.post(t, "/v1/work/bulk", bulkBody(`["crew-01"]`, "1", `"action":"assign","task":`+gotoOK), nil)
	if code != 200 || !strings.Contains(body, `"ok":false`) || !strings.Contains(body, "queue unavailable") {
		t.Fatalf("%d %s", code, body)
	}
}
