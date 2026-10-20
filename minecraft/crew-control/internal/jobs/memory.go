package jobs

import (
	"context"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Memory is the store for local runs and tests; it forgets everything on exit.
type Memory struct {
	mu   sync.Mutex
	jobs map[uuid.UUID]*memJob
	seq  []uuid.UUID
	now  func() time.Time
}

type memJob struct {
	lock sync.Mutex // held for a Lock call; try-locked so a busy job is skipped
	data sync.Mutex // guards job alone, so readers never wait for a scheduler pass
	job  Job
}

func NewMemory() *Memory {
	return &Memory{jobs: map[uuid.UUID]*memJob{}, now: time.Now}
}

func (m *Memory) Create(_ context.Context, j Job) (Job, error) {
	j, err := prepare(j)
	if err != nil {
		return Job{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j.Created, j.Updated = m.now(), m.now()
	m.jobs[j.ID] = &memJob{job: j}
	m.seq = append(m.seq, j.ID)
	return j.clone(), nil
}

func (m *Memory) find(id uuid.UUID) (*memJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mj, ok := m.jobs[id]
	return mj, ok
}

func (m *Memory) Running(_ context.Context) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []uuid.UUID
	for _, id := range m.seq {
		if mj := m.jobs[id]; mj != nil && mj.snapshot().State == JobRunning {
			out = append(out, id)
		}
	}
	return out, nil
}

func (mj *memJob) snapshot() Job {
	mj.data.Lock()
	defer mj.data.Unlock()
	return mj.job.clone()
}

func (mj *memJob) store(j Job) {
	mj.data.Lock()
	defer mj.data.Unlock()
	mj.job = j
}

func (m *Memory) Lock(_ context.Context, id uuid.UUID, fn func(*Job) error) (bool, error) {
	mj, ok := m.find(id)
	if !ok {
		return false, ErrNotFound
	}
	if !mj.lock.TryLock() {
		return false, nil
	}
	defer mj.lock.Unlock()
	j := mj.snapshot()
	before := j.clone()
	if err := fn(&j); err != nil {
		return true, err
	}
	if reflect.DeepEqual(j, before) {
		return true, nil
	}
	j.Updated = m.now()
	mj.store(j)
	return true, nil
}

func (m *Memory) Get(_ context.Context, id uuid.UUID) (Job, error) {
	mj, ok := m.find(id)
	if !ok {
		return Job{}, ErrNotFound
	}
	return mj.snapshot(), nil
}

func (m *Memory) List(_ context.Context, n int) ([]Job, error) {
	m.mu.Lock()
	ids := append([]uuid.UUID(nil), m.seq...)
	m.mu.Unlock()
	var out []Job
	for i := len(ids) - 1; i >= 0 && len(out) < n; i-- {
		if mj, ok := m.find(ids[i]); ok {
			out = append(out, mj.snapshot())
		}
	}
	return out, nil
}
