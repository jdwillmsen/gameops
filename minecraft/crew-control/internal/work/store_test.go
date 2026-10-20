package work

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	ctx  = context.Background()
	tpl  = json.RawMessage(`{"kind":"goto","x":1}`)
	stop = json.RawMessage(`{"name":"stop"}`)
)

func TestMemoryStore(t *testing.T) {
	suite(t, func(t *testing.T) Store { return NewMemory() },
		// The memory store takes its clock from Next's argument.
		func(_ *testing.T, _ Store, _ uuid.UUID, d time.Duration) time.Time { return time.Now().Add(d) })
}

func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("CREW_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("CREW_PG_TEST_DSN not set")
	}
	suite(t, func(t *testing.T) Store {
		p, err := OpenPostgres(ctx, dsn, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		// Every case uses its own unit names, but start from nothing so a
		// previous run cannot leak in.
		if _, err := p.Prune(ctx, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		return p
	}, func(t *testing.T, s Store, id uuid.UUID, d time.Duration) time.Time {
		// The database keeps its own clock, so age the row instead.
		_, err := s.(*Postgres).pool.Exec(ctx,
			`UPDATE `+workTable+` SET delivered_at = delivered_at - make_interval(secs => $2),
			 updated_at = updated_at - make_interval(secs => $2) WHERE id = $1`,
			id, d.Seconds())
		if err != nil {
			t.Fatal(err)
		}
		return time.Now()
	})
}

// age makes a delivered item d older and returns the now to hand Next.
func suite(t *testing.T, mk func(*testing.T) Store, age func(*testing.T, Store, uuid.UUID, time.Duration) time.Time) {
	cases := map[string]func(*testing.T, Store){
		"commands before tasks, FIFO within a kind": func(t *testing.T, s Store) {
			t1 := must(t, s, "crew-01", KindTask, tpl)
			t2 := must(t, s, "crew-01", KindTask, tpl)
			c1 := must(t, s, "crew-01", KindCommand, stop)
			now := time.Now()
			for i, want := range []uuid.UUID{c1.ID, t1.ID, t2.ID} {
				it, ok, _, err := s.Next(ctx, "crew-01", "", now)
				if err != nil || !ok || it.ID != want {
					t.Fatalf("pull %d: got %v ok=%v err=%v, want %v", i, it.ID, ok, err, want)
				}
				if it.State != Delivered {
					t.Fatalf("state %q, want delivered", it.State)
				}
			}
			if _, ok, _, _ := s.Next(ctx, "crew-01", "", now); ok {
				t.Fatal("queue should be drained")
			}
		},
		"command-only poll skips queued tasks": func(t *testing.T, s Store) {
			task := must(t, s, "crew-01", KindTask, tpl)
			command := must(t, s, "crew-01", KindCommand, stop)
			got, ok, _, err := s.Next(ctx, "crew-01", "command", time.Now())
			if err != nil || !ok || got.ID != command.ID || got.Kind != KindCommand {
				t.Fatalf("command-only poll: %+v ok=%v err=%v", got, ok, err)
			}
			if _, ok, _, err := s.Next(ctx, "crew-01", "command", time.Now()); err != nil || ok {
				t.Fatalf("command-only poll returned task %s: ok=%v err=%v", task.ID, ok, err)
			}
			if got, ok, _, err := s.Next(ctx, "crew-01", "", time.Now()); err != nil || !ok || got.ID != task.ID {
				t.Fatalf("unfiltered poll did not retain task: %+v ok=%v err=%v", got, ok, err)
			}
		},
		"CancelQueued cancels only while queued": func(t *testing.T, s Store) {
			a := must(t, s, "crew-01", KindTask, tpl)
			got, ok, err := s.CancelQueued(ctx, "crew-01", a.ID)
			if err != nil || !ok || got.State != Cancelled {
				t.Fatalf("queued: %+v ok=%v err=%v", got, ok, err)
			}
			b := must(t, s, "crew-01", KindTask, tpl)
			if it, ok, _, _ := s.Next(ctx, "crew-01", "", time.Now()); !ok || it.ID != b.ID {
				t.Fatalf("a cancelled item must not be handed out, got %v", it.ID)
			}
			got, ok, err = s.CancelQueued(ctx, "crew-01", b.ID)
			if err != nil || ok || got.State != Delivered {
				t.Fatalf("delivered: %+v ok=%v err=%v", got, ok, err)
			}
			if _, _, err := s.CancelQueued(ctx, "crew-02", b.ID); !errors.Is(err, ErrWrongUnit) {
				t.Fatalf("wrong unit: %v", err)
			}
			if _, _, err := s.CancelQueued(ctx, "crew-01", uuid.New()); !errors.Is(err, ErrNotFound) {
				t.Fatalf("unknown: %v", err)
			}
		},
		"units do not see each other's work": func(t *testing.T, s Store) {
			must(t, s, "crew-02", KindTask, tpl)
			if _, ok, _, _ := s.Next(ctx, "crew-03", "", time.Now()); ok {
				t.Fatal("crew-03 received crew-02 work")
			}
		},
		"redelivery after 30 seconds": func(t *testing.T, s Store) {
			it := must(t, s, "crew-01", KindTask, tpl)
			if _, ok, _, _ := s.Next(ctx, "crew-01", "", time.Now()); !ok {
				t.Fatal("first delivery missing")
			}
			now := age(t, s, it.ID, 20*time.Second)
			_, ok, due, _ := s.Next(ctx, "crew-01", "", now)
			if ok {
				t.Fatal("redelivered too early")
			}
			if due < 5*time.Second || due > 11*time.Second {
				t.Fatalf("redeliverIn %v, want about 10s", due)
			}
			now = age(t, s, it.ID, 31*time.Second)
			got, ok, _, err := s.Next(ctx, "crew-01", "", now)
			if err != nil || !ok || got.ID != it.ID {
				t.Fatalf("no redelivery: %v %v %v", got.ID, ok, err)
			}
		},
		"running items are re-handed only after a silence": func(t *testing.T, s Store) {
			it := must(t, s, "crew-01", KindTask, tpl)
			s.Next(ctx, "crew-01", "", time.Now())
			if _, err := s.Apply(ctx, "crew-01", it.ID, Running, 40, ""); err != nil {
				t.Fatal(err)
			}
			if _, ok, _, _ := s.Next(ctx, "crew-01", "", age(t, s, it.ID, 20*time.Second)); ok {
				t.Fatal("running item came back while its unit was reporting")
			}
			// A heartbeat from a live unit restarts the silence.
			if _, err := s.Apply(ctx, "crew-01", it.ID, Running, 40, ""); err != nil {
				t.Fatal(err)
			}
			if _, ok, _, _ := s.Next(ctx, "crew-01", "", age(t, s, it.ID, 20*time.Second)); ok {
				t.Fatal("heartbeat did not keep the item")
			}
		},
		"running item whose unit restarted is handed out again": func(t *testing.T, s Store) {
			it := must(t, s, "crew-01", KindTask, tpl)
			s.Next(ctx, "crew-01", "", time.Now())
			if _, err := s.Apply(ctx, "crew-01", it.ID, Running, 40, ""); err != nil {
				t.Fatal(err)
			}
			now := age(t, s, it.ID, 31*time.Second)
			got, ok, _, err := s.Next(ctx, "crew-01", "", now)
			if err != nil || !ok || got.ID != it.ID || got.State != Delivered {
				t.Fatalf("restarted unit got %+v %v %v", got, ok, err)
			}
		},
		"apply updates and terminal states are sticky": func(t *testing.T, s Store) {
			it := must(t, s, "crew-01", KindTask, tpl)
			got, err := s.Apply(ctx, "crew-01", it.ID, Running, 40, "")
			if err != nil || got.State != Running || got.Progress != 40 {
				t.Fatalf("running: %+v %v", got, err)
			}
			got, _ = s.Apply(ctx, "crew-01", it.ID, Failed, 40, "blocked")
			if got.State != Failed || got.Reason != "blocked" {
				t.Fatalf("failed: %+v", got)
			}
			got, err = s.Apply(ctx, "crew-01", it.ID, Running, 90, "")
			if err != nil || got.State != Failed || got.Progress != 40 {
				t.Fatalf("terminal changed: %+v %v", got, err)
			}
		},
		"apply refusals": func(t *testing.T, s Store) {
			it := must(t, s, "crew-01", KindTask, tpl)
			if _, err := s.Apply(ctx, "crew-02", it.ID, Running, 0, ""); !errors.Is(err, ErrWrongUnit) {
				t.Fatalf("wrong unit: %v", err)
			}
			if _, err := s.Apply(ctx, "crew-01", uuid.New(), Running, 0, ""); !errors.Is(err, ErrNotFound) {
				t.Fatalf("unknown id: %v", err)
			}
			if _, err := s.Apply(ctx, "crew-01", it.ID, "queued", 0, ""); !errors.Is(err, ErrInvalid) {
				t.Fatalf("bad state: %v", err)
			}
		},
		"enqueue refuses bodies a unit cannot decode": func(t *testing.T, s Store) {
			for _, c := range []struct {
				kind string
				body string
			}{
				{KindTask, `[1]`}, {KindTask, `"x"`}, {KindTask, `null`}, {KindTask, `{`},
				{KindCommand, `{"name":"restart"}`}, {KindCommand, `{}`},
				{KindCommand, `{"name":"stop","x":1}`}, {KindCommand, `[]`},
				{"other", `{}`},
			} {
				if _, err := s.Enqueue(ctx, "crew-01", c.kind, json.RawMessage(c.body), "op"); !errors.Is(err, ErrInvalid) {
					t.Errorf("%s %s: err %v, want ErrInvalid", c.kind, c.body, err)
				}
			}
		},
		"StopUnit cancels open work and queues stop atomically": func(t *testing.T, s Store) {
			a := must(t, s, "crew-01", KindTask, tpl)
			b := must(t, s, "crew-01", KindTask, tpl)
			other := must(t, s, "crew-02", KindTask, tpl)
			s.Apply(ctx, "crew-01", b.ID, Done, 100, "")
			got, command, _, stopped, err := s.StopUnit(ctx, "crew-01", "op", nil)
			if err != nil || !stopped || len(got) != 1 || got[0].ID != a.ID || got[0].State != Cancelled || command.Kind != KindCommand || command.State != Queued {
				t.Fatalf("cancelled %+v stop %+v stopped=%v err=%v", got, command, stopped, err)
			}
			recent, _ := s.Recent(ctx, "crew-02", 5)
			if len(recent) != 1 || recent[0].ID != other.ID || recent[0].State != Queued {
				t.Fatalf("other unit touched: %+v", recent)
			}
			if got, ok, _, _ := s.Next(ctx, "crew-01", "", time.Now()); !ok || got.ID != command.ID {
				t.Fatalf("stop command missing or not prioritized: %+v", got)
			}
		},
		"StopUnit does nothing when its conditional item is terminal": func(t *testing.T, s Store) {
			done := must(t, s, "crew-01", KindTask, tpl)
			other := must(t, s, "crew-01", KindTask, tpl)
			if _, err := s.Apply(ctx, "crew-01", done.ID, Done, 100, ""); err != nil {
				t.Fatal(err)
			}
			items, command, target, stopped, err := s.StopUnit(ctx, "crew-01", "op", &done.ID)
			if err != nil || stopped || len(items) != 0 || command.ID != uuid.Nil || target.State != Done {
				t.Fatalf("terminal conditional stop: items=%+v command=%+v target=%+v stopped=%v err=%v", items, command, target, stopped, err)
			}
			recent, _ := s.Recent(ctx, "crew-01", 5)
			if len(recent) != 2 || recent[0].ID != other.ID || recent[0].State != Queued {
				t.Fatalf("conditional stop changed unrelated work: %+v", recent)
			}
		},
		"EnqueueStep stores the job and step": func(t *testing.T, s Store) {
			job, step := uuid.New(), uuid.New()
			it, err := s.EnqueueStep(ctx, "crew-01", tpl, "op", job, step)
			if err != nil || it.Kind != KindTask || it.State != Queued || it.JobID == nil || *it.JobID != job || *it.StepID != step {
				t.Fatalf("%+v %v", it, err)
			}
			got, _ := s.Recent(ctx, "crew-01", 1)
			if got[0].JobID == nil || *got[0].JobID != job || *got[0].StepID != step {
				t.Fatalf("not stored: %+v", got[0])
			}
			plain := must(t, s, "crew-01", KindTask, tpl)
			if plain.JobID != nil || plain.StepID != nil {
				t.Fatalf("hand-queued work has a job: %+v", plain)
			}
			if _, err := s.EnqueueStep(ctx, "crew-01", json.RawMessage(`[]`), "op", job, step); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid body: %v", err)
			}
		},
		"EnqueueStep is idempotent while the step's item is open": func(t *testing.T, s Store) {
			job, step := uuid.New(), uuid.New()
			first, err := s.EnqueueStep(ctx, "crew-01", tpl, "op", job, step)
			if err != nil {
				t.Fatal(err)
			}
			again, err := s.EnqueueStep(ctx, "crew-01", tpl, "op", job, step)
			if err != nil || again.ID != first.ID {
				t.Fatalf("second enqueue = %+v %v, want %s", again, err, first.ID)
			}
			if got, _ := s.Recent(ctx, "crew-01", 10); len(got) != 1 {
				t.Fatalf("%d items", len(got))
			}
			if _, err := s.Apply(ctx, "crew-01", first.ID, Failed, 0, "x"); err != nil {
				t.Fatal(err)
			}
			next, err := s.EnqueueStep(ctx, "crew-01", tpl, "op", job, step)
			if err != nil || next.ID == first.ID || next.State != Queued {
				t.Fatalf("after terminal = %+v %v", next, err)
			}
		},
		"cancel_task commands validate": func(t *testing.T, s Store) {
			good := json.RawMessage(`{"name":"cancel_task","task":"` + uuid.NewString() + `"}`)
			if _, err := s.Enqueue(ctx, "crew-01", KindCommand, good, "op"); err != nil {
				t.Fatalf("good: %v", err)
			}
			for _, body := range []string{
				`{"name":"cancel_task"}`, `{"name":"cancel_task","task":"nope"}`,
				`{"name":"cancel_task","task":"` + uuid.Nil.String() + `"}`,
				`{"name":"cancel_task","task":"` + uuid.NewString() + `","x":1}`,
				`{"name":"stop","task":"` + uuid.NewString() + `"}`,
			} {
				if _, err := s.Enqueue(ctx, "crew-01", KindCommand, json.RawMessage(body), "op"); !errors.Is(err, ErrInvalid) {
					t.Errorf("%s: %v, want ErrInvalid", body, err)
				}
			}
		},
		"CancelTask cancels a queued task in place": func(t *testing.T, s Store) {
			a := must(t, s, "crew-01", KindTask, tpl)
			b := must(t, s, "crew-01", KindTask, tpl)
			it, cmd, err := s.CancelTask(ctx, "crew-01", a.ID, "op")
			if err != nil || cmd != nil || it.State != Cancelled {
				t.Fatalf("%+v %+v %v", it, cmd, err)
			}
			recent, _ := s.Recent(ctx, "crew-01", 5)
			if len(recent) != 2 || recent[0].ID != b.ID || recent[0].State != Queued {
				t.Fatalf("%+v", recent)
			}
		},
		"CancelTask on a delivered task queues cancel_task and leaves other work": func(t *testing.T, s Store) {
			a := must(t, s, "crew-01", KindTask, tpl)
			b := must(t, s, "crew-01", KindTask, tpl)
			if got, ok, _, _ := s.Next(ctx, "crew-01", "", time.Now()); !ok || got.ID != a.ID {
				t.Fatal("setup")
			}
			it, cmd, err := s.CancelTask(ctx, "crew-01", a.ID, "op2")
			if err != nil || cmd == nil || it.State != Delivered || cmd.Kind != KindCommand || cmd.State != Queued || cmd.Operator != "op2" ||
				!cancelsTask(cmd.Body, a.ID) {
				t.Fatalf("%+v %+v %v", it, cmd, err)
			}
			// Commands go first, so the unit hears about it before more work.
			if got, ok, _, _ := s.Next(ctx, "crew-01", "", time.Now()); !ok || got.ID != cmd.ID {
				t.Fatalf("cancel_task not delivered before tasks: %+v", got)
			}
			if got, ok, _, _ := s.Next(ctx, "crew-01", "", time.Now()); !ok || got.ID != b.ID || got.State != Delivered {
				t.Fatalf("other task disturbed: %+v", got)
			}
		},
		"CancelTask on a running task queues cancel_task": func(t *testing.T, s Store) {
			a := must(t, s, "crew-01", KindTask, tpl)
			s.Next(ctx, "crew-01", "", time.Now())
			s.Apply(ctx, "crew-01", a.ID, Running, 5, "")
			it, cmd, err := s.CancelTask(ctx, "crew-01", a.ID, "op")
			if err != nil || cmd == nil || it.State != Running {
				t.Fatalf("%+v %+v %v", it, cmd, err)
			}
		},
		"CancelTask returns a finished task unchanged": func(t *testing.T, s Store) {
			a := must(t, s, "crew-01", KindTask, tpl)
			s.Apply(ctx, "crew-01", a.ID, Done, 100, "")
			it, cmd, err := s.CancelTask(ctx, "crew-01", a.ID, "op")
			if err != nil || cmd != nil || it.State != Done {
				t.Fatalf("%+v %+v %v", it, cmd, err)
			}
			if recent, _ := s.Recent(ctx, "crew-01", 5); len(recent) != 1 {
				t.Fatalf("queued something: %+v", recent)
			}
		},
		"CancelTask refusals": func(t *testing.T, s Store) {
			a := must(t, s, "crew-01", KindTask, tpl)
			if _, _, err := s.CancelTask(ctx, "crew-01", uuid.New(), "op"); !errors.Is(err, ErrNotFound) {
				t.Errorf("unknown: %v", err)
			}
			if _, _, err := s.CancelTask(ctx, "crew-02", a.ID, "op"); !errors.Is(err, ErrWrongUnit) {
				t.Errorf("other unit: %v", err)
			}
			c := must(t, s, "crew-01", KindCommand, stop)
			s.Next(ctx, "crew-01", "command", time.Now())
			if _, cmd, err := s.CancelTask(ctx, "crew-01", c.ID, "op"); !errors.Is(err, ErrInvalid) || cmd != nil {
				t.Errorf("delivered command: %v", err)
			}
		},
		"CancelJob cancels only that job's open items": func(t *testing.T, s Store) {
			job, other := uuid.New(), uuid.New()
			queued, _ := s.EnqueueStep(ctx, "crew-01", tpl, "op", job, uuid.New())
			taken, _ := s.EnqueueStep(ctx, "crew-02", tpl, "op", job, uuid.New())
			finished, _ := s.EnqueueStep(ctx, "crew-02", tpl, "op", job, uuid.New())
			s.Apply(ctx, "crew-02", finished.ID, Done, 100, "")
			elsewhere, _ := s.EnqueueStep(ctx, "crew-01", tpl, "op", other, uuid.New())
			byHand := must(t, s, "crew-01", KindTask, tpl)
			if got, ok, _, _ := s.Next(ctx, "crew-02", "", time.Now()); !ok || got.ID != taken.ID {
				t.Fatal("setup")
			}
			got, err := s.CancelJob(ctx, job, "op")
			if err != nil || len(got) != 2 {
				t.Fatalf("%+v %v", got, err)
			}
			states := map[uuid.UUID]string{}
			for _, it := range got {
				states[it.ID] = it.State
			}
			if states[queued.ID] != Cancelled || states[taken.ID] != Delivered {
				t.Fatalf("%+v", states)
			}
			r1, _ := s.Recent(ctx, "crew-01", 10)
			for _, it := range r1 {
				switch it.ID {
				case byHand.ID, elsewhere.ID:
					if it.State != Queued {
						t.Errorf("untouched item changed: %+v", it)
					}
				}
			}
			r2, _ := s.Recent(ctx, "crew-02", 10)
			if len(r2) != 3 || r2[0].Kind != KindCommand || !cancelsTask(r2[0].Body, taken.ID) {
				t.Fatalf("no cancel_task for the taken step: %+v", r2)
			}
			if again, err := s.CancelJob(ctx, job, "op"); err != nil || len(again) != 1 {
				t.Fatalf("second cancel: %+v %v", again, err)
			}
		},
		"cancel_task done cancels a task the unit no longer holds": func(t *testing.T, s Store) {
			a := must(t, s, "crew-01", KindTask, tpl)
			s.Next(ctx, "crew-01", "", time.Now())
			_, cmd, err := s.CancelTask(ctx, "crew-01", a.ID, "op")
			if err != nil || cmd == nil {
				t.Fatal(err)
			}
			if got, ok, _, _ := s.Next(ctx, "crew-01", "", time.Now()); !ok || got.ID != cmd.ID {
				t.Fatal("setup")
			}
			if _, err := s.Apply(ctx, "crew-01", cmd.ID, Done, 100, ""); err != nil {
				t.Fatal(err)
			}
			got, _ := s.Get(ctx, a.ID)
			if got.State != Cancelled {
				t.Fatalf("task %s, want cancelled", got.State)
			}
			now := age(t, s, a.ID, time.Hour)
			if again, ok, _, _ := s.Next(ctx, "crew-01", "", now); ok {
				t.Fatalf("handed out again: %+v", again)
			}
		},
		"cancel_task done leaves a finished task alone": func(t *testing.T, s Store) {
			a := must(t, s, "crew-01", KindTask, tpl)
			s.Apply(ctx, "crew-01", a.ID, Done, 100, "")
			c := must(t, s, "crew-01", KindCommand, json.RawMessage(`{"name":"cancel_task","task":"`+a.ID.String()+`"}`))
			s.Next(ctx, "crew-01", "", time.Now())
			if _, err := s.Apply(ctx, "crew-01", c.ID, Done, 100, ""); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(ctx, a.ID); got.State != Done {
				t.Fatalf("task %s, want done", got.State)
			}
		},
		"an open cancel_task blocks redelivery of its task": func(t *testing.T, s Store) {
			a := must(t, s, "crew-01", KindTask, tpl)
			s.Next(ctx, "crew-01", "", time.Now())
			_, cmd, _ := s.CancelTask(ctx, "crew-01", a.ID, "op")
			if got, ok, _, _ := s.Next(ctx, "crew-01", "", time.Now()); !ok || got.ID != cmd.ID {
				t.Fatal("setup")
			}
			now := age(t, s, a.ID, time.Hour)
			if got, ok, _, _ := s.Next(ctx, "crew-01", "", now); ok && got.ID == a.ID {
				t.Fatal("the task was redelivered under a pending cancel")
			}
		},
		"a cancel_task that fails makes its task deliverable again": func(t *testing.T, s Store) {
			a := must(t, s, "crew-01", KindTask, tpl)
			s.Next(ctx, "crew-01", "", time.Now())
			_, cmd, _ := s.CancelTask(ctx, "crew-01", a.ID, "op")
			if got, ok, _, _ := s.Next(ctx, "crew-01", "", time.Now()); !ok || got.ID != cmd.ID {
				t.Fatal("setup")
			}
			// An older unit fails the command it does not know.
			if _, err := s.Apply(ctx, "crew-01", cmd.ID, Failed, 0, "unknown command"); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(ctx, a.ID); got.State == Cancelled {
				t.Fatal("a failed cancel_task cancelled its target")
			}
			now := age(t, s, a.ID, time.Hour)
			if got, ok, _, _ := s.Next(ctx, "crew-01", "", now); !ok || got.ID != a.ID {
				t.Fatalf("the task was not redelivered: %+v %v", got, ok)
			}
		},
		"Get finds an item or reports not found": func(t *testing.T, s Store) {
			a := must(t, s, "crew-01", KindTask, tpl)
			if got, err := s.Get(ctx, a.ID); err != nil || got.ID != a.ID {
				t.Fatalf("%+v %v", got, err)
			}
			if _, err := s.Get(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
				t.Fatalf("%v", err)
			}
		},
		"recent is newest first and limited": func(t *testing.T, s Store) {
			var ids []uuid.UUID
			for range 4 {
				ids = append(ids, must(t, s, "crew-01", KindTask, tpl).ID)
				time.Sleep(2 * time.Millisecond)
			}
			got, err := s.Recent(ctx, "crew-01", 3)
			if err != nil || len(got) != 3 {
				t.Fatalf("len %d err %v", len(got), err)
			}
			for i, it := range got {
				if it.ID != ids[3-i] {
					t.Fatalf("position %d: %v, want %v", i, it.ID, ids[3-i])
				}
			}
		},
		"open lists a unit's unfinished items newest first": func(t *testing.T, s Store) {
			a := must(t, s, "crew-07", KindTask, tpl)
			b := must(t, s, "crew-07", KindTask, tpl)
			c := must(t, s, "crew-07", KindTask, tpl)
			must(t, s, "crew-08", KindTask, tpl)
			if _, err := s.Apply(ctx, "crew-07", a.ID, Done, 100, ""); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.CancelTask(ctx, "crew-07", c.ID, "op"); err != nil {
				t.Fatal(err)
			}
			d := must(t, s, "crew-07", KindTask, tpl)
			got, err := s.Open(ctx, "crew-07")
			if err != nil || len(got) != 2 || got[0].ID != d.ID || got[1].ID != b.ID {
				t.Fatalf("got %v err=%v, want [%v %v]", got, err, d.ID, b.ID)
			}
			none, err := s.Open(ctx, "crew-09")
			if err != nil || len(none) != 0 {
				t.Fatalf("idle unit: %v %v", none, err)
			}
		},
		"prune": func(t *testing.T, s Store) {
			must(t, s, "crew-01", KindTask, tpl)
			if n, err := s.Prune(ctx, time.Now().Add(-time.Hour)); err != nil || n != 0 {
				t.Fatalf("pruned fresh rows: %d %v", n, err)
			}
			if n, err := s.Prune(ctx, time.Now().Add(time.Hour)); err != nil || n != 1 {
				t.Fatalf("prune: %d %v", n, err)
			}
			if got, _ := s.Recent(ctx, "crew-01", 5); len(got) != 0 {
				t.Fatalf("still there: %+v", got)
			}
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) { fn(t, mk(t)) })
	}
}

func must(t *testing.T, s Store, unit, kind string, body json.RawMessage) Item {
	t.Helper()
	it, err := s.Enqueue(ctx, unit, kind, body, "op")
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func TestLazyAnnouncesWhenTheStoreIsSet(t *testing.T) {
	var l Lazy
	calls := 0
	l.OnReady(func() { calls++ })
	if _, err := l.Recent(ctx, "crew-01", 1); !errors.Is(err, ErrUnavailable) || calls != 0 {
		t.Fatalf("before Set: %v, %d calls", err, calls)
	}
	l.Set(NewMemory())
	if calls != 1 {
		t.Fatalf("OnReady ran %d times", calls)
	}
	if _, err := l.Recent(ctx, "crew-01", 1); err != nil {
		t.Fatal(err)
	}
}

// cancelsTask compares as JSON: Postgres stores bodies as jsonb and returns
// them re-spaced.
func cancelsTask(body json.RawMessage, id uuid.UUID) bool {
	var c map[string]string
	return json.Unmarshal(body, &c) == nil && len(c) == 2 && c["name"] == "cancel_task" && c["task"] == id.String()
}
