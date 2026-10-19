package tagmerge

import (
	"sync"

	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	coretagmerge "github.com/perber/wiki/internal/tagmerge"
)

// Phase marks the current stage of a tag merge run.
type Phase string

const (
	PhaseCompile Phase = "compile"
	PhaseApply   Phase = "apply"
)

// JobStatus is the snapshot returned by GET .../tag-merge/status.
type JobStatus struct {
	Running          bool                    `json:"running"`
	Phase            string                  `json:"phase,omitempty"`
	Done             bool                    `json:"done"`
	Error            string                  `json:"error,omitempty"`
	ErrorCode        string                  `json:"errorCode,omitempty"`
	TotalPages       int                     `json:"totalPages"`
	ScannedPages     int                     `json:"scannedPages"`
	ChangedPages     int                     `json:"changedPages"`
	UnsupportedPages int                     `json:"unsupportedPages"`
	FailedPages      int                     `json:"failedPages"`
	Conflicts        []coretagmerge.Conflict `json:"conflicts,omitempty"`
	Cycles           [][]string              `json:"cycles,omitempty"`
}

// MergeJob tracks the lifecycle of one background tag merge run. Safe for
// concurrent use.
type MergeJob struct {
	mu     sync.RWMutex
	status JobStatus
}

// NewMergeJob constructs an idle MergeJob.
func NewMergeJob() *MergeJob {
	return &MergeJob{}
}

// Start marks a run as started and resets all state from the previous run.
// Returns false when a run is already in progress.
func (j *MergeJob) Start() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.status.Running {
		return false
	}
	j.status = JobStatus{Running: true}
	return true
}

// SetPhase records the current worker phase.
func (j *MergeJob) SetPhase(p Phase) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status.Phase = string(p)
}

// SetTotalPages records the number of pages the run will scan.
func (j *MergeJob) SetTotalPages(n int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status.TotalPages = n
}

func (j *MergeJob) add(delta func(*JobStatus)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delta(&j.status)
}

// AddScanned advances the scanned-page counter.
func (j *MergeJob) AddScanned(n int) {
	j.add(func(s *JobStatus) { s.ScannedPages += n })
}

// AddChanged advances the changed-page counter.
func (j *MergeJob) AddChanged(n int) {
	j.add(func(s *JobStatus) { s.ChangedPages += n })
}

// AddUnsupported advances the unsupported-page counter.
func (j *MergeJob) AddUnsupported(n int) {
	j.add(func(s *JobStatus) { s.UnsupportedPages += n })
}

// AddFailed advances the failed-page counter.
func (j *MergeJob) AddFailed(n int) {
	j.add(func(s *JobStatus) { s.FailedPages += n })
}

// FinishCompileError ends a run that never wrote anything because the rule
// set did not compile.
func (j *MergeJob) FinishCompileError(e *coretagmerge.CompileError) {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := &j.status
	s.Running = false
	s.Done = true
	s.Phase = ""
	s.ErrorCode = ErrCodeConflict
	s.Error = e.Error()
	s.Conflicts = e.Conflicts
	s.Cycles = e.Cycles
}

// Finish ends the run, recording err (nil on success) and any localized code.
func (j *MergeJob) Finish(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := &j.status
	s.Running = false
	s.Done = true
	s.Phase = ""
	if err != nil {
		s.Error = err.Error()
		if loc, ok := sharederrors.AsLocalizedError(err); ok {
			s.ErrorCode = loc.Code
		}
	}
}

// Status returns a point-in-time copy of the job state.
func (j *MergeJob) Status() JobStatus {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.status
}
