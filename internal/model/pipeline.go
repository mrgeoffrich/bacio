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
// order. New presets are added here; nothing else changes.
var pipelineProcesses = []Process{
	{Slug: "plan-implement-ship", Name: "Plan → Implement → Ship", Stages: []string{BuiltinTemplatePlan, BuiltinTemplateImplement, ShipJobMode}},
	{Slug: "implement-ship", Name: "Implement → Ship", Stages: []string{BuiltinTemplateImplement, ShipJobMode}},
	{Slug: "plan-implement", Name: "Plan → Implement", Stages: []string{BuiltinTemplatePlan, BuiltinTemplateImplement}},
	{Slug: "plan", Name: "Plan", Stages: []string{BuiltinTemplatePlan}},
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
