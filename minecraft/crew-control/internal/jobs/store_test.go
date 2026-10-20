package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ctx = context.Background()

func TestMemoryStore(t *testing.T) {
	storeSuite(t, func(t *testing.T) Store { return NewMemory() })
}

func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("CREW_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("CREW_PG_TEST_DSN not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	storeSuite(t, func(t *testing.T) Store {
		if _, err := pool.Exec(ctx, `DELETE FROM minecraft.crew_job`); err != nil {
			t.Fatal(err)
		}
		return NewPostgres(pool)
	})
}

func step(key string, deps ...string) Step {
	return Step{Key: key, Template: json.RawMessage(`{"template":"` + key + `"}`), DependsOn: deps}
}

func newJob(steps ...Step) Job {
	return Job{Kind: KindPlan, Spec: json.RawMessage(`{"a":1}`), Operator: "ada", Deadline: time.Now().Add(time.Hour), Steps: steps}
}

func byKey(j Job, key string) Step {
	for _, s := range j.Steps {
		if s.Key == key {
			return s
		}
	}
	return Step{}
}

func storeSuite(t *testing.T, mk func(*testing.T) Store) {
	cases := map[string]func(*testing.T, Store){
		"create stores the job with waiting steps": func(t *testing.T, s Store) {
			pinned := step("a")
			pinned.Unit = "crew-01"
			j, err := s.Create(ctx, newJob(pinned, step("b", "a")))
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.Get(ctx, j.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != JobRunning || got.Operator != "ada" || len(got.Steps) != 2 || got.Created.IsZero() {
				t.Fatalf("job = %+v", got)
			}
			a, b := byKey(got, "a"), byKey(got, "b")
			if a.State != StepWaiting || a.Unit != "crew-01" || b.Unit != "" || len(b.DependsOn) != 1 || b.DependsOn[0] != "a" {
				t.Fatalf("steps = %+v", got.Steps)
			}
			if string(a.Template) != `{"template": "a"}` && string(a.Template) != `{"template":"a"}` {
				t.Fatalf("template = %s", a.Template)
			}
		},
		"create rejects bad graphs": func(t *testing.T, s Store) {
			for name, steps := range map[string][]Step{
				"duplicate": {step("a"), step("a")},
				"unknown":   {step("a", "zzz")},
				"cycle":     {step("a", "b"), step("b", "a")},
				"self":      {step("a", "a")},
				"no key":    {step("")},
			} {
				if _, err := s.Create(ctx, newJob(steps...)); !errors.Is(err, ErrInvalid) {
					t.Errorf("%s: err = %v", name, err)
				}
			}
		},
		"get of a missing job": func(t *testing.T, s Store) {
			if _, err := s.Get(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v", err)
			}
			if _, err := s.Lock(ctx, uuid.New(), func(*Job) error { return nil }); !errors.Is(err, ErrNotFound) {
				t.Fatalf("lock err = %v", err)
			}
		},
		"a lock that changes nothing leaves updated_at alone": func(t *testing.T, s Store) {
			j, _ := s.Create(ctx, newJob(step("a")))
			before, _ := s.Get(ctx, j.ID)
			time.Sleep(20 * time.Millisecond)
			if ok, err := s.Lock(ctx, j.ID, func(*Job) error { return nil }); err != nil || !ok {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
			after, _ := s.Get(ctx, j.ID)
			if !after.Updated.Equal(before.Updated) {
				t.Fatalf("updated moved from %v to %v on an idle pass", before.Updated, after.Updated)
			}
			s.Lock(ctx, j.ID, func(j *Job) error { j.Steps[0].Attempts = 1; return nil })
			changed, _ := s.Get(ctx, j.ID)
			if byKey(changed, "a").Attempts != 1 {
				t.Fatalf("a real change was not stored: %+v", changed)
			}
		},
		"lock stores changes, including appended steps": func(t *testing.T, s Store) {
			j, _ := s.Create(ctx, newJob(step("a")))
			w := uuid.New()
			ok, err := s.Lock(ctx, j.ID, func(j *Job) error {
				j.Steps[0].State, j.Steps[0].Attempts, j.Steps[0].WorkID, j.Steps[0].Reason = StepQueued, 1, &w, "r"
				j.Round, j.Reason = 1, "why"
				n := step("c", "a")
				n.ID, n.State, n.DependsOn = uuid.New(), StepWaiting, []string{"a"}
				j.Steps = append(j.Steps, n)
				return nil
			})
			if err != nil || !ok {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
			got, _ := s.Get(ctx, j.ID)
			a := byKey(got, "a")
			if a.State != StepQueued || a.Attempts != 1 || a.WorkID == nil || *a.WorkID != w || a.Reason != "r" ||
				got.Round != 1 || got.Reason != "why" || len(got.Steps) != 2 || byKey(got, "c").State != StepWaiting {
				t.Fatalf("job = %+v", got)
			}
			if !got.Updated.After(j.Created) && got.Updated.Before(j.Created) {
				t.Fatalf("updated %v before created %v", got.Updated, j.Created)
			}
		},
		"a failing lock discards its changes": func(t *testing.T, s Store) {
			j, _ := s.Create(ctx, newJob(step("a")))
			boom := errors.New("boom")
			_, err := s.Lock(ctx, j.ID, func(j *Job) error { j.State = JobFailed; return boom })
			if !errors.Is(err, boom) {
				t.Fatalf("err = %v", err)
			}
			if got, _ := s.Get(ctx, j.ID); got.State != JobRunning {
				t.Fatalf("state = %s", got.State)
			}
		},
		"running lists running jobs oldest first": func(t *testing.T, s Store) {
			a, _ := s.Create(ctx, newJob(step("a")))
			time.Sleep(5 * time.Millisecond)
			b, _ := s.Create(ctx, newJob(step("a")))
			time.Sleep(5 * time.Millisecond)
			c, _ := s.Create(ctx, newJob(step("a")))
			_, _ = s.Lock(ctx, b.ID, func(j *Job) error { j.State = JobDone; return nil })
			ids, err := s.Running(ctx)
			if err != nil || len(ids) != 2 || ids[0] != a.ID || ids[1] != c.ID {
				t.Fatalf("ids = %v err = %v", ids, err)
			}
			list, err := s.List(ctx, 2)
			if err != nil || len(list) != 2 || list[0].ID != c.ID || list[1].ID != b.ID || len(list[0].Steps) != 1 {
				t.Fatalf("list = %+v err = %v", list, err)
			}
		},
		"a locked job is skipped": func(t *testing.T, s Store) {
			j, _ := s.Create(ctx, newJob(step("a")))
			in, release := make(chan struct{}), make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = s.Lock(ctx, j.ID, func(*Job) error { close(in); <-release; return nil })
			}()
			<-in
			ok, err := s.Lock(ctx, j.ID, func(*Job) error { t.Error("second lock ran"); return nil })
			if err != nil || ok {
				t.Errorf("second lock ok=%v err=%v", ok, err)
			}
			if got, err := s.Get(ctx, j.ID); err != nil || got.ID != j.ID {
				t.Errorf("get while locked: %v", err)
			}
			close(release)
			wg.Wait()
			if ok, _ := s.Lock(ctx, j.ID, func(*Job) error { return nil }); !ok {
				t.Error("lock after release not granted")
			}
		},
		"concurrent locks grant exactly one": func(t *testing.T, s Store) {
			j, _ := s.Create(ctx, newJob(step("a")))
			const callers = 6
			var (
				wg      sync.WaitGroup
				granted atomic.Int32
				skipped = make(chan struct{}, callers)
				hold    = make(chan struct{})
			)
			for range callers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					ok, _ := s.Lock(ctx, j.ID, func(*Job) error {
						granted.Add(1)
						<-hold
						return nil
					})
					if !ok {
						skipped <- struct{}{}
					}
				}()
			}
			// Everyone but the holder must come back refused before it lets go.
			for range callers - 1 {
				select {
				case <-skipped:
				case <-time.After(10 * time.Second):
					t.Fatal("a caller was not refused")
				}
			}
			close(hold)
			wg.Wait()
			if granted.Load() != 1 {
				t.Fatalf("granted = %d", granted.Load())
			}
		},
		"a job without a deadline gets the default": func(t *testing.T, s Store) {
			j := newJob(step("a"))
			j.Deadline = time.Time{}
			got, err := s.Create(ctx, j)
			if err != nil {
				t.Fatal(err)
			}
			stored, _ := s.Get(ctx, got.ID)
			if d := time.Until(stored.Deadline); d < DefaultTimeout-time.Minute || d > DefaultTimeout+time.Minute {
				t.Fatalf("deadline in %v", d)
			}
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) { fn(t, mk(t)) })
	}
}
