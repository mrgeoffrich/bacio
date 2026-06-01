package model

import (
	"fmt"
	"strings"
	"time"
)

// JobStatus is the lifecycle of a single pipeline job — one stage in a
// card's process chain. The controller engine owns every transition:
// pending → running (the engine queued a dispatch for the stage) →
// complete (that dispatch acked) | cancelled (Stop/Cancel). Agents do
// not write this; it is the engine's source of truth for "where in the
// chain is this card".
type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobComplete  JobStatus = "complete"
	JobCancelled JobStatus = "cancelled"
)

var allJobStatuses = []JobStatus{JobPending, JobRunning, JobComplete, JobCancelled}

// AllJobStatuses returns a copy of the job-status set.
func AllJobStatuses() []JobStatus { return append([]JobStatus(nil), allJobStatuses...) }

// ParseJobStatus validates a job-status string at the store boundary.
func ParseJobStatus(s string) (JobStatus, error) {
	norm := strings.ToLower(strings.TrimSpace(s))
	for _, st := range allJobStatuses {
		if string(st) == norm {
			return st, nil
		}
	}
	return "", fmt.Errorf("unknown job status %q", s)
}

// Terminal reports whether the job has finished (complete or cancelled)
// and the engine should not act on it again.
func (s JobStatus) Terminal() bool { return s == JobComplete || s == JobCancelled }

// EngineMode is the per-issue controller-engine drive mode while the
// card is in_pipeline. "off" = the user advances one job at a time via
// Start; "auto" = the engine runs the chain consecutively, halting on an
// open question and resuming when it is answered.
type EngineMode string

const (
	EngineOff  EngineMode = "off"
	EngineAuto EngineMode = "auto"
)

var allEngineModes = []EngineMode{EngineOff, EngineAuto}

// ParseEngineMode validates an engine-mode string at the store boundary.
// The empty string is read as EngineOff so a freshly-dropped card (no
// engine field written yet) reads as "manual".
func ParseEngineMode(s string) (EngineMode, error) {
	norm := strings.ToLower(strings.TrimSpace(s))
	if norm == "" {
		return EngineOff, nil
	}
	for _, m := range allEngineModes {
		if string(m) == norm {
			return m, nil
		}
	}
	return "", fmt.Errorf("unknown engine mode %q", s)
}

// EnginePauseReasonOpenQuestion: the current job has an open question, so
// Auto will not advance until it is answered (§6.1 of the requirements).
const EnginePauseReasonOpenQuestion = "open_question"

// EnginePauseReasonAgentErrorTransient / ...Terminal: the worker running
// the current job died on an Anthropic API error (BACI-296). Either way
// the engine cancels the in-flight job and halts Auto in place rather
// than re-queuing — the user re-arms with Start/Auto. The card stays
// in_pipeline for both classes (BACI-300 retired the old needs_action
// hand-off); the two values exist only so the Pipeline UI can word the
// halt differently:
//
//   - Transient (server_error / rate_limit) — an outage. "Start to
//     retry once it clears."
//   - Terminal (auth / billing / org-not-allowed / unknown) — the user
//     must fix an account/config problem before a retry can succeed.
//
// model.IsTransientAPIError is the single source of truth for the branch.
const (
	EnginePauseReasonAgentErrorTransient = "agent_error_transient"
	EnginePauseReasonAgentErrorTerminal  = "agent_error_terminal"
)

// EnginePauseReasonSubagentCancelled: the user soft-cancelled (Esc /
// interrupt) the dispatch worker running the current job, so control
// returned to the supervisor session, which called the
// `report_subagent_incomplete` channel tool (BACI-328). Like the API-error
// reasons it cancels the in-flight job + dispatch and halts Auto in place
// — the card stays in_pipeline — but it is a *deliberate user cancel*, not
// a failure: the Pipeline UI renders a neutral "Cancelled" halt (not the
// red error styling), worded "Start to retry". Distinct from the two
// agent_error_* classes, which mean the worker died on a transport error.
const EnginePauseReasonSubagentCancelled = "subagent_cancelled"

// ShipJobMode is the sentinel "mode" for the Ship hand-off stage inside
// a process chain. It is deliberately the same slug as the ship dispatch
// template, but a pipeline job carrying it is a HAND-OFF, not a dispatch:
// the engine recognises it and moves the card to to_be_shipped instead
// of queuing an agent. The ship *agent* is dispatched separately from
// the Shipping column (§4 / impact-analysis §0), never as a pipeline job.
const ShipJobMode = BuiltinTemplateShip

// ShelveJobMode is the sentinel "mode" for the Shelve hand-off stage —
// the demoting counterpart to ShipJobMode (BACI-332). Unlike Ship it is
// NOT a builtin template slug (shelve never dispatches an agent): a
// pipeline job carrying it is a HAND-OFF the engine recognises and uses
// to move the card back to Backlog (todo), clearing the chain so the
// demoted card is indistinguishable from one dragged back to Backlog by
// hand and is freely re-pipeable. Final-stage only, same as Ship.
const ShelveJobMode = "shelve"

// sentinelLabels are the display labels for the non-dispatch terminal
// sentinels (ship / shelve), consulted by stageDisplay before the raw
// mode fallback so audit/display text reads "Scope → Shelve" rather than
// "scope → shelve". Dispatch stages get their labels from
// BuiltinTemplateActionLabel.
var sentinelLabels = map[string]string{
	ShipJobMode:   "Ship",
	ShelveJobMode: "Shelve",
}

// PipelineJob is one persisted stage of a card's process chain
// (pipeline_jobs row). Mode is a dispatch-template slug (plan,
// implement, …) or ShipJobMode for the hand-off. DispatchID points at
// the agent_dispatches row once the engine queues the stage (nil while
// pending; ON DELETE SET NULL so a retention prune of the dispatch
// leaves the job's history intact).
type PipelineJob struct {
	ID          int64      `json:"id"`
	IssueID     int64      `json:"issue_id"`
	Sequence    int        `json:"sequence"`
	Mode        string     `json:"mode"`
	Status      JobStatus  `json:"status"`
	DispatchID  *int64     `json:"dispatch_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// IsShipHandoff reports whether this stage is the Ship hand-off (move to
// to_be_shipped) rather than an agent dispatch.
func (j PipelineJob) IsShipHandoff() bool { return j.Mode == ShipJobMode }

// IsShelveHandoff reports whether this stage is the Shelve hand-off (move
// back to todo / Backlog) rather than an agent dispatch.
func (j PipelineJob) IsShelveHandoff() bool { return j.Mode == ShelveJobMode }

// Process is a named preset chain selected when a card enters
// in_pipeline (§5.1). The presets are an in-code enumeration, not stored
// — only the materialised pipeline_jobs rows persist. Stages is the
// ordered list of job modes; a trailing ShipJobMode marks the hand-off.
type Process struct {
	Slug   string   `json:"slug"`
	Name   string   `json:"name"`
	Stages []string `json:"stages"`
}

// pipelineProcesses is the starter set of processes (§5.1), in menu
// order. New presets are added here; nothing else changes. The slug
// path is retained for CLI ergonomics / back-compat — the desktop
// picker now builds an explicit ordered stage list via ProcessFromStages
// for arbitrary combinations, but the named presets still name the common
// chains for `bacio issue process set <KEY> <preset>`. KEEP IN LOCKSTEP
// with desktop/frontend/src/lib/pipelineProcesses.ts (PIPELINE_PROCESSES)
// — same slugs, stages, order.
var pipelineProcesses = []Process{
	{Slug: "plan-implement-ship", Name: "Plan → Implement → Ship", Stages: []string{BuiltinTemplatePlan, BuiltinTemplateImplement, ShipJobMode}},
	{Slug: "implement-ship", Name: "Implement → Ship", Stages: []string{BuiltinTemplateImplement, ShipJobMode}},
	{Slug: "plan-implement", Name: "Plan → Implement", Stages: []string{BuiltinTemplatePlan, BuiltinTemplateImplement}},
	{Slug: "plan", Name: "Plan", Stages: []string{BuiltinTemplatePlan}},
	{Slug: "plan_large", Name: "Large Plan", Stages: []string{BuiltinTemplatePlanLarge}},
	{Slug: "design", Name: "Design", Stages: []string{BuiltinTemplateDesign}},
	{Slug: "implement", Name: "Implement", Stages: []string{BuiltinTemplateImplement}},
	// Triage stages (BACI-300): standalone, no-chain passes that run a
	// single agent and finish in place. Reachable from the Pipeline
	// picker as standalone rows, replacing the retired manual-triage
	// dispatch path.
	{Slug: "scope", Name: "Scope", Stages: []string{BuiltinTemplateScope}},
	// scope-shelve (BACI-332): the out-of-the-box chain for a freshly-piped
	// Pipeline-screen issue — run one Scope pass, then park the card back in
	// Backlog at the Shelve sentinel for the human to triage the result.
	{Slug: "scope-shelve", Name: "Scope → Shelve", Stages: []string{BuiltinTemplateScope, ShelveJobMode}},
	{Slug: "research", Name: "Research", Stages: []string{BuiltinTemplateResearch}},
}

// PipelineProcesses returns a copy of the preset process list.
func PipelineProcesses() []Process {
	out := make([]Process, len(pipelineProcesses))
	copy(out, pipelineProcesses)
	return out
}

// ProcessBySlug resolves a preset by slug at the store/API boundary.
func ProcessBySlug(slug string) (Process, error) {
	norm := strings.ToLower(strings.TrimSpace(slug))
	for _, p := range pipelineProcesses {
		if p.Slug == norm {
			return p, nil
		}
	}
	return Process{}, fmt.Errorf("unknown pipeline process %q", slug)
}

// stageModeAllowed reports whether a (lowercased, trimmed) stage mode is a
// valid non-ship chain stage: a dispatchable built-in template slug. The
// reserved _dispatch_preamble (and any other leading-underscore reserved
// slug) is excluded — it is the wrapper prepended to every dispatch, never
// a stage a user can put in a chain. BuiltinTemplateSlugs() leads with it,
// so building the allowed set off the slug list alone would (wrongly)
// accept it; gating on a non-empty action label is the same predicate the
// dispatch dropdown uses (BuiltinTemplateActionLabel returns "" only for
// _dispatch_preamble).
func stageModeAllowed(mode string) bool {
	if mode == ShipJobMode || mode == ShelveJobMode {
		return true
	}
	return BuiltinTemplateActionLabel(mode) != ""
}

// normaliseStages lowercases, trims, and drops blank entries from a raw
// stage list — the shared front-half of both stage-list constructors.
func normaliseStages(stages []string) []string {
	norm := make([]string, 0, len(stages))
	for _, s := range stages {
		m := strings.ToLower(strings.TrimSpace(s))
		if m != "" {
			norm = append(norm, m)
		}
	}
	return norm
}

// stageDisplay synthesises the audit/display Slug (modes joined with "-")
// and Name (action labels joined with " → ") for a normalised mode list.
// Display-only — nothing keys off them.
func stageDisplay(modes []string) (slug, name string) {
	labels := make([]string, len(modes))
	for i, m := range modes {
		switch {
		case sentinelLabels[m] != "":
			labels[i] = sentinelLabels[m]
		case BuiltinTemplateActionLabel(m) != "":
			labels[i] = BuiltinTemplateActionLabel(m)
		default:
			labels[i] = m
		}
	}
	return strings.Join(modes, "-"), strings.Join(labels, " → ")
}

// ProcessFromStages builds a Process from an explicit ordered stage list
// — the construction path behind the desktop cumulative-stepper picker,
// which can express arbitrary chains the named presets don't enumerate
// (e.g. design → plan_large → implement → ship). It is the single
// validation gate for a free-form chain, mirroring ProcessBySlug's role
// for the named ones: every stage must be a dispatchable builtin template
// slug or the Ship hand-off sentinel; Ship may appear only as the trailing
// stage; no duplicate non-ship modes; the list must be non-empty. The
// synthesised Slug (modes joined with "-") and Name (action labels joined
// with " → ") are for audit / display only — nothing keys off them.
func ProcessFromStages(stages []string) (Process, error) {
	norm := normaliseStages(stages)
	if len(norm) == 0 {
		return Process{}, fmt.Errorf("stages must list at least one job mode")
	}
	seen := make(map[string]bool, len(norm))
	for i, m := range norm {
		if m == ShipJobMode {
			if i != len(norm)-1 {
				return Process{}, fmt.Errorf("ship may only be the final stage")
			}
			continue
		}
		if m == ShelveJobMode {
			if i != len(norm)-1 {
				return Process{}, fmt.Errorf("shelve may only be the final stage")
			}
			continue
		}
		if !stageModeAllowed(m) {
			return Process{}, fmt.Errorf("unknown job mode %q", m)
		}
		if seen[m] {
			return Process{}, fmt.Errorf("duplicate job mode %q", m)
		}
		seen[m] = true
	}
	slug, name := stageDisplay(norm)
	return Process{Slug: slug, Name: name, Stages: norm}, nil
}

// ProcessFromStagesWithPrefix validates an edited chain that keeps an
// immutable locked prefix (the completed / running / cancelled jobs the
// engine has already advanced past) and replaces the editable pending tail
// — the construction path behind the BACI-294 Edit Process screen. It
// validates the *combined* prefix+tail chain: every non-ship mode must be
// a dispatchable builtin (ship excepted), and Ship may appear only as the
// final stage of the whole chain. Unlike ProcessFromStages it does NOT
// reject duplicate modes — a re-loop (Implement → Review → Implement) is a
// deliberately expressible chain on the edit path. At least one of prefix
// or tail must be non-empty.
//
// The returned Process.Stages is the validated TAIL ONLY (re-sequenced
// after the prefix by the store, which is the source of truth for the
// locked rows); Slug/Name describe the whole chain for audit/display.
func ProcessFromStagesWithPrefix(prefixModes, tailModes []string) (Process, error) {
	prefix := normaliseStages(prefixModes)
	tail := normaliseStages(tailModes)
	combined := append(append([]string(nil), prefix...), tail...)
	if len(combined) == 0 {
		return Process{}, fmt.Errorf("stages must list at least one job mode")
	}
	for i, m := range combined {
		if m == ShipJobMode {
			if i != len(combined)-1 {
				return Process{}, fmt.Errorf("ship may only be the final stage")
			}
			continue
		}
		if m == ShelveJobMode {
			if i != len(combined)-1 {
				return Process{}, fmt.Errorf("shelve may only be the final stage")
			}
			continue
		}
		if !stageModeAllowed(m) {
			return Process{}, fmt.Errorf("unknown job mode %q", m)
		}
	}
	slug, name := stageDisplay(combined)
	return Process{Slug: slug, Name: name, Stages: tail}, nil
}

// ResolveProcess picks the right constructor for the `issue process set`
// payload: exactly one of slug / stages must be set (mutually
// exclusive), and the resulting Process is the materialisation source.
// Centralised here so the API handler and the local client share one
// both/neither guard rather than each re-deriving it.
func ResolveProcess(slug string, stages []string) (Process, error) {
	hasSlug := strings.TrimSpace(slug) != ""
	hasStages := len(stages) > 0
	switch {
	case hasSlug && hasStages:
		return Process{}, fmt.Errorf("process and stages are mutually exclusive")
	case hasStages:
		return ProcessFromStages(stages)
	case hasSlug:
		return ProcessBySlug(slug)
	default:
		return Process{}, fmt.Errorf("either process (preset slug) or stages (ordered list) is required")
	}
}
