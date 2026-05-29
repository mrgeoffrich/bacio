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

// EnginePauseReasonOpenQuestion is the only pause reason today: the
// current job has an open question, so Auto will not advance until it is
// answered (§6.1 of the requirements).
const EnginePauseReasonOpenQuestion = "open_question"

// ShipJobMode is the sentinel "mode" for the Ship hand-off stage inside
// a process chain. It is deliberately the same slug as the ship dispatch
// template, but a pipeline job carrying it is a HAND-OFF, not a dispatch:
// the engine recognises it and moves the card to to_be_shipped instead
// of queuing an agent. The ship *agent* is dispatched separately from
// the Shipping column (§4 / impact-analysis §0), never as a pipeline job.
const ShipJobMode = BuiltinTemplateShip

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

// ProcessFromStages builds a Process from an explicit ordered stage list
// — the construction path behind the desktop cumulative-stepper picker,
// which can express arbitrary chains the named presets don't enumerate
// (e.g. design → plan_large → implement → ship). It is the single
// validation gate for a free-form chain, mirroring ProcessBySlug's role
// for the named ones: every stage must be a known builtin template slug
// or the Ship hand-off sentinel; Ship may appear only as the trailing
// stage; no duplicate non-ship modes; the list must be non-empty. The
// synthesised Slug (modes joined with "-") and Name (action labels joined
// with " → ") are for audit / display only — nothing keys off them.
func ProcessFromStages(stages []string) (Process, error) {
	norm := make([]string, 0, len(stages))
	for _, s := range stages {
		m := strings.ToLower(strings.TrimSpace(s))
		if m != "" {
			norm = append(norm, m)
		}
	}
	if len(norm) == 0 {
		return Process{}, fmt.Errorf("stages must list at least one job mode")
	}
	allowed := make(map[string]bool, len(builtinTemplateSlugs))
	for _, s := range BuiltinTemplateSlugs() {
		allowed[s] = true
	}
	seen := make(map[string]bool, len(norm))
	for i, m := range norm {
		if m == ShipJobMode {
			if i != len(norm)-1 {
				return Process{}, fmt.Errorf("ship may only be the final stage")
			}
			continue
		}
		if !allowed[m] {
			return Process{}, fmt.Errorf("unknown job mode %q", m)
		}
		if seen[m] {
			return Process{}, fmt.Errorf("duplicate job mode %q", m)
		}
		seen[m] = true
	}
	labels := make([]string, len(norm))
	for i, m := range norm {
		if lbl := BuiltinTemplateActionLabel(m); lbl != "" {
			labels[i] = lbl
		} else {
			labels[i] = m
		}
	}
	return Process{
		Slug:   strings.Join(norm, "-"),
		Name:   strings.Join(labels, " → "),
		Stages: norm,
	}, nil
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
