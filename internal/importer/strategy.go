package importer

// ImportMode selects the reconciliation strategy behind a plan/execution.
// The zero value and ModeLegacy both denote the historical one-shot import
// (create missing pages, skip existing ones), so persisted plans written
// before modes existed deserialize as legacy.
type ImportMode string

const (
	ModeLegacy  ImportMode = "legacy"
	ModeManaged ImportMode = "managed"
)

// normalizeMode maps the persisted/requested mode to a supported value.
// An empty string means legacy (backward compatibility); any other unknown
// value is rejected rather than silently treated as legacy.
func normalizeMode(mode ImportMode) (ImportMode, error) {
	switch mode {
	case "", ModeLegacy:
		return ModeLegacy, nil
	case ModeManaged:
		return ModeManaged, nil
	default:
		return "", ErrUnknownImportMode
	}
}

// PlanExecutor is the execution surface shared by mode strategies.
// The builder methods return the interface so per-mode executors can be
// configured fluently by ImporterService.
type PlanExecutor interface {
	Execute(userID string) (*ExecutionResult, error)
	WithProgressCallback(progressFn func(ExecutionProgress, *ExecutionResult)) PlanExecutor
	WithCancelCheck(cancelFn func() bool) PlanExecutor
	WithResumeState(startIndex int, initialResult *ExecutionResult) PlanExecutor
}

// ModeStrategy encapsulates how a given ImportMode turns extracted entries
// into a plan and how a stored plan is executed. ImporterService owns all
// cross-mode concerns (workspace cleanup, persistence, resume scheduling).
type ModeStrategy interface {
	Mode() ImportMode
	CreatePlan(entries []ImportMDFile, opts PlanOptions) (*PlanResult, error)
	NewExecutor(sp *StoredPlan, assetMaxBytes int64) PlanExecutor
}

// legacyStrategy preserves the historical behavior verbatim: Planner builds
// create/skip items and Executor only creates missing pages.
type legacyStrategy struct {
	planner *Planner
}

func newLegacyStrategy(planner *Planner) *legacyStrategy {
	return &legacyStrategy{planner: planner}
}

func (s *legacyStrategy) Mode() ImportMode {
	return ModeLegacy
}

func (s *legacyStrategy) CreatePlan(entries []ImportMDFile, opts PlanOptions) (*PlanResult, error) {
	return s.planner.CreatePlan(entries, opts)
}

func (s *legacyStrategy) NewExecutor(sp *StoredPlan, assetMaxBytes int64) PlanExecutor {
	return NewExecutor(sp.Plan, &sp.PlanOptions, assetMaxBytes, s.planner.wiki, s.planner.log)
}
