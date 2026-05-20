package usageforecast

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// JobStore persists asynchronous forecast job state.
type JobStore interface {
	Create(ctx context.Context, job *schema.TokenForecastJob) error
	Get(ctx context.Context, id string) (*schema.TokenForecastJob, error)
	Update(ctx context.Context, job *schema.TokenForecastJob) error
}

// MemoryJobStore is an in-process store for spike and local development.
type MemoryJobStore struct {
	mu   sync.RWMutex
	jobs map[string]*schema.TokenForecastJob
}

// NewMemoryJobStore creates an empty forecast job store.
func NewMemoryJobStore() *MemoryJobStore {
	return &MemoryJobStore{jobs: map[string]*schema.TokenForecastJob{}}
}

// Create stores a new job.
func (s *MemoryJobStore) Create(_ context.Context, job *schema.TokenForecastJob) error {
	if job == nil {
		return errors.New("job is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *job
	s.jobs[job.ID] = &cp
	return nil
}

// Get returns a job by ID.
func (s *MemoryJobStore) Get(_ context.Context, id string) (*schema.TokenForecastJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	if !ok {
		return nil, errors.New("forecast job not found")
	}
	cp := *job
	return &cp, nil
}

// Update replaces a job.
func (s *MemoryJobStore) Update(_ context.Context, job *schema.TokenForecastJob) error {
	if job == nil {
		return errors.New("job is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[job.ID]; !ok {
		return errors.New("forecast job not found")
	}
	cp := *job
	s.jobs[job.ID] = &cp
	return nil
}

// JobOrchestrator schedules and resolves asynchronous forecast jobs.
type JobOrchestrator struct {
	service *Service
	store   JobStore
	queue   chan string
	now     func() time.Time
}

// NewJobOrchestrator creates a local worker-backed orchestrator.
func NewJobOrchestrator(service *Service, store JobStore, queueSize int) *JobOrchestrator {
	if queueSize <= 0 {
		queueSize = 128
	}
	return &JobOrchestrator{
		service: service,
		store:   store,
		queue:   make(chan string, queueSize),
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// Enqueue creates a queued job and schedules it for background execution.
func (o *JobOrchestrator) Enqueue(ctx context.Context, params schema.TokenForecastJobParams) (*schema.TokenForecastJob, error) {
	if o == nil || o.store == nil {
		return nil, errors.New("forecast job orchestrator is not configured")
	}
	if params.UserID == "" {
		return nil, errors.New("user_id is required")
	}
	now := o.now()
	job := &schema.TokenForecastJob{
		ID:        "fcst_" + randomID(),
		Status:    schema.ForecastJobQueued,
		Request:   params,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := o.store.Create(ctx, job); err != nil {
		return nil, err
	}
	select {
	case o.queue <- job.ID:
		return job, nil
	default:
		job.Status = schema.ForecastJobFailed
		job.Error = "forecast job queue is full"
		job.UpdatedAt = o.now()
		_ = o.store.Update(ctx, job)
		return job, errors.New(job.Error)
	}
}

// Get returns the current state of a job.
func (o *JobOrchestrator) Get(ctx context.Context, id string) (*schema.TokenForecastJob, error) {
	if o == nil || o.store == nil {
		return nil, errors.New("forecast job orchestrator is not configured")
	}
	return o.store.Get(ctx, id)
}

// Start runs worker goroutines until ctx is cancelled.
func (o *JobOrchestrator) Start(ctx context.Context, workers int) {
	if o == nil || o.service == nil || o.store == nil {
		return
	}
	if workers <= 0 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		go o.runWorker(ctx)
	}
}

func (o *JobOrchestrator) runWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-o.queue:
			o.runJob(ctx, id)
		}
	}
}

func (o *JobOrchestrator) runJob(ctx context.Context, id string) {
	job, err := o.store.Get(ctx, id)
	if err != nil {
		return
	}
	job.Status = schema.ForecastJobRunning
	job.UpdatedAt = o.now()
	_ = o.store.Update(ctx, job)

	resp, err := o.service.Forecast(ctx, requestFromJobParams(job.Request))
	job.UpdatedAt = o.now()
	if err != nil {
		job.Status = schema.ForecastJobFailed
		job.Error = err.Error()
		_ = o.store.Update(ctx, job)
		return
	}
	job.Status = schema.ForecastJobSucceeded
	job.Result = resp
	_ = o.store.Update(ctx, job)
}

func requestFromJobParams(params schema.TokenForecastJobParams) schema.TokenForecastRequest {
	return schema.TokenForecastRequest{
		UserID:         params.UserID,
		Model:          params.Model,
		Window:         durationFromHours(params.WindowHours),
		Horizon:        durationFromHours(params.HorizonHours),
		Bucket:         durationFromMinutes(params.BucketMinutes),
		FreeTokenLimit: params.FreeTokenLimit,
	}
}

func durationFromHours(hours float64) time.Duration {
	if hours <= 0 {
		return 0
	}
	return time.Duration(hours * float64(time.Hour))
}

func durationFromMinutes(minutes float64) time.Duration {
	if minutes <= 0 {
		return 0
	}
	return time.Duration(minutes * float64(time.Minute))
}

func randomID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(b[:])
}
