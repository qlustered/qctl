package rule_run_replays

import (
	"fmt"
	"strconv"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dry_runs"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// Progress describes a replay's current carrier attempt.
type Progress string

const (
	// ProgressRunning is a carrier outside the dry-run watch stop states.
	ProgressRunning Progress = "running"
	// ProgressFinished is a carrier in the finished state.
	ProgressFinished Progress = "finished"
	// ProgressFailed is a carrier in a watch stop state other than finished.
	ProgressFailed Progress = "failed"
	// ProgressUnknown is a missing or unrecognized carrier state.
	ProgressUnknown Progress = "unknown"
)

// runningStates are the known carrier states outside the dry-run watch stop set.
var runningStates = map[api.IngestionJobState]bool{
	api.IngestionJobStateCreated:  true,
	api.IngestionJobStatePending:  true,
	api.IngestionJobStateRunning:  true,
	api.IngestionJobStateRequeued: true,
	api.IngestionJobStateStaged:   true,
}

// Classify maps the current carrier state onto a Progress using the dry-run
// watch stop set. A failed attempt may later be retried.
func Classify(state *api.IngestionJobState) Progress {
	if state == nil {
		return ProgressUnknown
	}
	switch {
	case *state == api.IngestionJobStateFinished:
		return ProgressFinished
	case dry_runs.IsTerminal(*state):
		return ProgressFailed
	case runningStates[*state]:
		return ProgressRunning
	default:
		return ProgressUnknown
	}
}

// ReportNote explains an absent report for p.
func ReportNote(p Progress) string {
	switch p {
	case ProgressRunning:
		return "the replay has not finished yet, so no report has been written"
	case ProgressFailed:
		return "the replay's carrier stopped without finishing; no report was written for this attempt"
	case ProgressFinished:
		return "the replay finished but wrote no report"
	case ProgressUnknown:
		return "no carrier state is available, so whether this replay is still running cannot be told from here"
	default:
		return ""
	}
}

// ReplaySpec is the launch's bookkeeping: what was asked for, and against what.
type ReplaySpec struct {
	ID             int    `yaml:"id" json:"id"`
	RuleRunID      int    `yaml:"rule_run_id" json:"rule_run_id"`
	DatasetID      int    `yaml:"dataset_id" json:"dataset_id"`
	DryRunJobID    *int   `yaml:"dry_run_job_id,omitempty" json:"dry_run_job_id,omitempty"`
	RowsInEvidence int    `yaml:"rows_in_evidence" json:"rows_in_evidence"`
	RowsPinned     int    `yaml:"rows_pinned" json:"rows_pinned"`
	RequestedByID  string `yaml:"requested_by_id,omitempty" json:"requested_by_id,omitempty"`
}

// ReplayStatus is what the replay has done so far.
type ReplayStatus struct {
	State           string                  `yaml:"state" json:"state"`
	Progress        Progress                `yaml:"progress" json:"progress"`
	RowsReplayed    int                     `yaml:"rows_replayed" json:"rows_replayed"`
	ReplayRuleRunID *int                    `yaml:"replay_rule_run_id,omitempty" json:"replay_rule_run_id,omitempty"`
	CreatedAt       string                  `yaml:"created_at" json:"created_at"`
	FinishedAt      string                  `yaml:"finished_at,omitempty" json:"finished_at,omitempty"`
	Report          *map[string]interface{} `yaml:"report,omitempty" json:"report,omitempty"`
	ReportNote      string                  `yaml:"report_note,omitempty" json:"report_note,omitempty"`
}

// ReplayManifest is the declarative view describe renders.
type ReplayManifest struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata `yaml:"metadata" json:"metadata"`
	Spec       ReplaySpec           `yaml:"spec" json:"spec"`
	Status     ReplayStatus         `yaml:"status" json:"status"`
}

// ManifestKind is the kind describe stamps on the view. It is NOT an `apply -f`
// kind: a replay is started with `qctl run rule-run-replay`, never applied.
const ManifestKind = "RuleRunReplay"

// APIResponseToManifest converts one replay into the describe view.
func APIResponseToManifest(r *Replay) *ReplayManifest {
	state := ""
	if r.State != nil {
		state = string(*r.State)
	}
	progress := Classify(r.State)

	note := ""
	if r.Report == nil {
		note = ReportNote(progress)
	}

	requestedBy := ""
	if r.RequestedByID != nil {
		requestedBy = r.RequestedByID.String()
	}

	labels := map[string]string{
		"rule_run_id": strconv.Itoa(r.RuleRunID),
		"dataset_id":  strconv.Itoa(r.DatasetID),
	}
	if r.DryRunJobID != nil {
		labels["dry_run_job_id"] = strconv.Itoa(*r.DryRunJobID)
	}

	return &ReplayManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       ManifestKind,
		Metadata: pkgmanifest.Metadata{
			Name:        fmt.Sprintf("rule-run-replay-%d", r.ID),
			Labels:      labels,
			Annotations: map[string]string{"rule_run_replay_id": strconv.Itoa(r.ID)},
		},
		Spec: ReplaySpec{
			ID:             r.ID,
			RuleRunID:      r.RuleRunID,
			DatasetID:      r.DatasetID,
			DryRunJobID:    r.DryRunJobID,
			RowsInEvidence: r.RowsInEvidence,
			RowsPinned:     r.RowsPinned,
			RequestedByID:  requestedBy,
		},
		Status: ReplayStatus{
			State:           state,
			Progress:        progress,
			RowsReplayed:    r.RowsReplayed,
			ReplayRuleRunID: r.ReplayRuleRunID,
			CreatedAt:       formatTime(&r.CreatedAt),
			FinishedAt:      formatTime(r.FinishedAt),
			Report:          r.Report,
			ReportNote:      note,
		},
	}
}

func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}
