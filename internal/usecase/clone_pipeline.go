package usecase

import (
	"context"
	"strings"
	"sync"
	"workspace-tool/internal/domain"
	"workspace-tool/internal/infrastructure"
)

// FailureAction defines the choice when a clone job fails.
type FailureAction string

const (
	ActionRetry FailureAction = "retry"
	ActionSkip  FailureAction = "skip"
)

// ClonePipeline orchestrates sequential execution of cloning jobs.
type ClonePipeline struct {
	mu             sync.RWMutex
	gitExecutor    *infrastructure.GitExecutor
	logger         *infrastructure.Logger
	jobs           []*domain.CloneJob
	activeJob      *domain.CloneJob
	eventChan      chan domain.CloneProgressEvent
	failureHandler func(job domain.CloneJob, err error) FailureAction
	cancelFunc     context.CancelFunc
	ctx            context.Context
	isRunning      bool
}

// NewClonePipeline initializes a new ClonePipeline.
func NewClonePipeline(executor *infrastructure.GitExecutor) *ClonePipeline {
	return &ClonePipeline{
		gitExecutor: executor,
		logger:      infrastructure.GetGlobalLogger(),
		jobs:        []*domain.CloneJob{},
		eventChan:   make(chan domain.CloneProgressEvent, 100),
	}
}

// RegisterFailureHandler sets the callback that handles Pause/Resume for Retry/Skip options.
func (p *ClonePipeline) RegisterFailureHandler(handler func(job domain.CloneJob, err error) FailureAction) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failureHandler = handler
}

// AddJob adds a repository to the pipeline queue.
func (p *ClonePipeline) AddJob(repo domain.Repo, targetPath string) *domain.CloneJob {
	p.mu.Lock()
	defer p.mu.Unlock()

	job := &domain.CloneJob{
		ID:         repo.ID,
		Repo:       repo,
		State:      domain.StatePending,
		TargetPath: targetPath,
	}
	p.jobs = append(p.jobs, job)
	return job
}

// GetJobs returns the list of all jobs currently in the pipeline.
func (p *ClonePipeline) GetJobs() []domain.CloneJob {
	p.mu.RLock()
	defer p.mu.RUnlock()

	jobs := make([]domain.CloneJob, len(p.jobs))
	for i, j := range p.jobs {
		jobs[i] = *j
	}
	return jobs
}

// Start launches the sequential worker pipeline, returning the event stream channel.
func (p *ClonePipeline) Start(parentCtx context.Context) <-chan domain.CloneProgressEvent {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isRunning {
		return p.eventChan
	}

	p.isRunning = true
	p.ctx, p.cancelFunc = context.WithCancel(parentCtx)

	go p.runWorker()

	return p.eventChan
}

// Cancel triggers context cancellation to abort any running git execution.
func (p *ClonePipeline) Cancel() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cancelFunc != nil {
		p.cancelFunc()
	}
}

func (p *ClonePipeline) runWorker() {
	defer func() {
		p.mu.Lock()
		p.isRunning = false
		close(p.eventChan)
		p.mu.Unlock()
	}()

	for i := 0; i < len(p.jobs); i++ {
		job := p.jobs[i]

		// Ensure pipeline was not cancelled before starting this job
		if p.ctx.Err() != nil {
			p.updateJobState(job, domain.StateCancelled, "Cancelled")
			continue
		}

		p.mu.Lock()
		p.activeJob = job
		p.mu.Unlock()

		p.updateJobState(job, domain.StateCloning, "Cloning repository...")

		success := false
		for !success {
			// Execute Clone command with active context
			err := p.gitExecutor.Clone(p.ctx, *job, func(event domain.CloneProgressEvent) {
				p.eventChan <- event
			})

			if err == nil {
				p.updateJobState(job, domain.StateSuccess, "Successfully cloned")
				success = true
			} else {
				// Handle clone failure
				if p.ctx.Err() != nil || strings.Contains(err.Error(), infrastructure.ErrCloneCancelled) {
					p.updateJobState(job, domain.StateCancelled, "Cancelled by user")
					return // Stop whole pipeline
				}

				p.updateJobState(job, domain.StateFailed, err.Error())

				// Pause and query failure handler
				p.mu.RLock()
				handler := p.failureHandler
				p.mu.RUnlock()

				action := ActionSkip
				if handler != nil {
					// Blocking call to get user decision
					p.logger.Info("Job %s failed. Prompting user for recovery action...", job.Repo.Name)
					action = handler(*job, err)
				} else {
					p.logger.Warn("No failure handler registered, defaulting to Skip.")
				}

				if action == ActionRetry {
					p.updateJobState(job, domain.StatePending, "Retrying...")
					// Loop again on the same job
					continue
				} else {
					// ActionSkip: Mark job as FAILED and proceed to the next in queue
					p.updateJobState(job, domain.StateFailed, err.Error())
					break
				}
			}
		}
	}
}

func (p *ClonePipeline) updateJobState(job *domain.CloneJob, state domain.CloneJobState, message string) {
	p.mu.Lock()
	job.State = state
	job.ErrorMessage = ""
	if state == domain.StateFailed {
		job.ErrorMessage = message
	}
	p.mu.Unlock()

	var eventType string
	var errCode *string
	switch state {
	case domain.StateCloning:
		eventType = "JOB_STARTED"
	case domain.StateSuccess:
		eventType = "JOB_COMPLETED"
	case domain.StateFailed:
		eventType = "JOB_FAILED"
		msgParts := strings.Split(message, ":")
		if len(msgParts) > 0 {
			code := strings.TrimSpace(msgParts[0])
			errCode = &code
		}
	case domain.StateCancelled:
		eventType = "JOB_FAILED"
		code := infrastructure.ErrCloneCancelled
		errCode = &code
	default:
		eventType = "CLONE_PROGRESS"
	}

	event := domain.CreateProgressEvent(eventType, job.Repo.ID, job.Repo.Name, state, message, errCode)
	p.eventChan <- event
}
