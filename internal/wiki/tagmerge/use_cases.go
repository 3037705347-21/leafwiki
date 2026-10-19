package tagmerge

import (
	"context"
	"errors"

	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	coretagmerge "github.com/perber/wiki/internal/tagmerge"
)

// ─── Rules view model ────────────────────────────────────────────────────────

// RulesOutput is the persisted rules plus any compile problems so the UI can
// show conflicts without triggering an apply.
type RulesOutput struct {
	Rules     []coretagmerge.Rule     `json:"rules"`
	Conflicts []coretagmerge.Conflict `json:"conflicts,omitempty"`
	Cycles    [][]string              `json:"cycles,omitempty"`
}

func buildRulesOutput(svc *coretagmerge.Service) RulesOutput {
	cfg := svc.Rules()
	out := RulesOutput{Rules: cfg.Rules}
	if out.Rules == nil {
		out.Rules = []coretagmerge.Rule{}
	}
	if _, err := coretagmerge.Compile(cfg); err != nil {
		var ce *coretagmerge.CompileError
		if errors.As(err, &ce) {
			out.Conflicts = ce.Conflicts
			out.Cycles = ce.Cycles
		}
	}
	return out
}

// ─── GetRulesUseCase ─────────────────────────────────────────────────────────

// GetRulesUseCase returns the persisted rules and any compile problems.
type GetRulesUseCase struct {
	svc *coretagmerge.Service
}

// NewGetRulesUseCase constructs a GetRulesUseCase.
func NewGetRulesUseCase(svc *coretagmerge.Service) *GetRulesUseCase {
	return &GetRulesUseCase{svc: svc}
}

// Execute returns the current rules.
func (uc *GetRulesUseCase) Execute(_ context.Context) RulesOutput {
	return buildRulesOutput(uc.svc)
}

// ─── UpdateRulesUseCase ──────────────────────────────────────────────────────

// UpdateRulesInput carries the rule set to persist.
type UpdateRulesInput struct {
	Rules []coretagmerge.Rule
}

// UpdateRulesUseCase validates, normalizes and persists the rule set.
type UpdateRulesUseCase struct {
	svc *coretagmerge.Service
}

// NewUpdateRulesUseCase constructs an UpdateRulesUseCase.
func NewUpdateRulesUseCase(svc *coretagmerge.Service) *UpdateRulesUseCase {
	return &UpdateRulesUseCase{svc: svc}
}

// Execute saves the rules and returns the resulting view model.
func (uc *UpdateRulesUseCase) Execute(_ context.Context, in UpdateRulesInput) (RulesOutput, error) {
	rules := in.Rules
	if rules == nil {
		rules = []coretagmerge.Rule{}
	}
	if err := uc.svc.Save(coretagmerge.Config{Rules: rules}); err != nil {
		return RulesOutput{}, err
	}
	return buildRulesOutput(uc.svc), nil
}

// ─── TriggerApplyUseCase ─────────────────────────────────────────────────────

// TriggerApplyUseCase pre-compiles the rules, starts the job and launches it.
type TriggerApplyUseCase struct {
	svc     *coretagmerge.Service
	job     *MergeJob
	trigger func()
}

// NewTriggerApplyUseCase constructs a TriggerApplyUseCase.
func NewTriggerApplyUseCase(svc *coretagmerge.Service, job *MergeJob, trigger func()) *TriggerApplyUseCase {
	return &TriggerApplyUseCase{svc: svc, job: job, trigger: trigger}
}

// Execute refuses to start when the rules do not compile (so an ambiguous
// rule set changes no files) or when a run is already in progress.
func (uc *TriggerApplyUseCase) Execute(_ context.Context) error {
	if _, err := coretagmerge.Compile(uc.svc.Rules()); err != nil {
		var ce *coretagmerge.CompileError
		if errors.As(err, &ce) {
			return sharederrors.NewLocalizedError(
				ErrCodeConflict,
				"Tag merge rules contain conflicts",
				"tag merge rules contain conflicts",
				ce,
			)
		}
		return err
	}
	if !uc.job.Start() {
		return ErrAlreadyRunning
	}
	uc.trigger()
	return nil
}

// ─── GetStatusUseCase ────────────────────────────────────────────────────────

// GetStatusUseCase returns the current merge job state.
type GetStatusUseCase struct {
	job *MergeJob
}

// NewGetStatusUseCase constructs a GetStatusUseCase.
func NewGetStatusUseCase(job *MergeJob) *GetStatusUseCase {
	return &GetStatusUseCase{job: job}
}

// Execute returns the job status snapshot.
func (uc *GetStatusUseCase) Execute(_ context.Context) JobStatus {
	return uc.job.Status()
}
