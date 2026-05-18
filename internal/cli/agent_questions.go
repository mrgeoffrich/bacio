package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// agentQuestionsCmd groups the BACI-53 questions verbs under
// `bacio agent questions`. They round-trip the ask_user_question
// pipeline from the CLI so the same flow is testable headless.
func agentQuestionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "questions",
		Short: "Inspect and answer ask_user_question rows (BACI-53)",
		Long: `The bacio MCP ask_user_question tool (see SKILL.md) parks an
agent's clarification question in agent_session_questions until
the user submits an answer via the TUI / desktop / web modal.
These verbs let you drive the same flow from the CLI — useful
for tests, scripts, and "I'm running headless on the CI box"
flows. The desktop and web bundle drive the same store rows
through the REST surface (Phase 4).

Local-only in v1 — remote mode returns ErrLocalOnly.`,
	}
	cmd.AddCommand(
		agentQuestionsListCmd(),
		agentQuestionsShowCmd(),
		agentQuestionsAnswerCmd(),
		agentQuestionsCancelCmd(),
	)
	return cmd
}

func agentQuestionsListCmd() *cobra.Command {
	var (
		sessionID, stateStr, rawInput string
		allSessions, allStates        bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List ask_user_question rows for a session (defaults to open state)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "session", "all_sessions", "states")
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.AgentQuestionsListInput](raw)
				if err != nil {
					return err
				}
				return runAgentQuestionsList(*in)
			}
			states := []string{}
			if stateStr != "" {
				states = []string{stateStr}
			}
			if allStates {
				states = []string{"open", "answered", "cancelled", "abandoned"}
			}
			in := inputs.AgentQuestionsListInput{
				AllSessions: allSessions,
				States:      states,
			}
			if !allSessions {
				sid, err := resolveSessionID(sessionID)
				if err != nil {
					return err
				}
				in.SessionID = sid
			}
			return runAgentQuestionsList(in)
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id (default: $CLAUDE_CODE_SESSION_ID)")
	cmd.Flags().BoolVar(&allSessions, "all-sessions", false, "list questions for every session (reserved — local lookups walk a single session at a time)")
	cmd.Flags().StringVar(&stateStr, "state", "", "filter to one state: open|answered|cancelled|abandoned (default: open)")
	cmd.Flags().BoolVar(&allStates, "all-states", false, "list every state (equivalent to --state open,answered,cancelled,abandoned)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runAgentQuestionsList(in inputs.AgentQuestionsListInput) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	if in.AllSessions {
		// v1 doesn't have a cross-session list at the store layer
		// (questions are session-keyed by design — a cross-session
		// list would need to walk every alive session). Refuse
		// rather than silently returning nothing.
		return fmt.Errorf("agent questions list --all-sessions is not implemented in v1 (pass --session <id> or set $CLAUDE_CODE_SESSION_ID)")
	}
	if in.SessionID == "" {
		return fmt.Errorf("session id is required (pass --session <id> or set $CLAUDE_CODE_SESSION_ID)")
	}
	states, err := parseQuestionStates(in.States)
	if err != nil {
		return err
	}
	rows, err := c.ListSessionQuestions(context.Background(), in.SessionID, states)
	if err != nil {
		return err
	}
	if opts.output == outputJSON && rows == nil {
		rows = []*model.SessionQuestion{}
	}
	return emit(rows)
}

func agentQuestionsShowCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one ask_user_question row (including the questions payload + any answer)",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.AgentQuestionsShowInput](raw)
				if err != nil {
					return err
				}
				return runAgentQuestionsShow(*in)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <id> positional or --json")
			}
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid question id %q: %w", args[0], err)
			}
			return runAgentQuestionsShow(inputs.AgentQuestionsShowInput{ID: id})
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runAgentQuestionsShow(in inputs.AgentQuestionsShowInput) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	q, err := c.GetSessionQuestion(context.Background(), in.ID)
	if err != nil {
		return err
	}
	return emit(q)
}

func agentQuestionsAnswerCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "answer",
		Short: "Submit an answer for an open ask_user_question — agent receives it as the tool result",
		Long: `Answers map question text (verbatim from the stored payload) to
either a string (single-select) or an array of strings (multi-
select). Unknown question keys, mismatched value types, and
already-answered/cancelled rows are rejected.

This is the CLI counterpart to clicking "Submit" in the TUI /
desktop / web modal — the next channel poll tick delivers the
answer back to the agent's parked ask_user_question call.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw == nil {
				return fmt.Errorf("requires --json '{\"id\":N,\"answers\":{...}}'")
			}
			in, _, err := inputio.DecodeStrict[inputs.AgentQuestionsAnswerInput](raw)
			if err != nil {
				return err
			}
			return runAgentQuestionsAnswer(*in)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runAgentQuestionsAnswer(in inputs.AgentQuestionsAnswerInput) error {
	if in.ID == 0 {
		return fmt.Errorf("question id is required")
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	q, err := c.AnswerSessionQuestion(context.Background(), in.ID, model.QuestionAnswers(in.Answers), opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(q)
	}
	return emit(q)
}

func agentQuestionsCancelCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "cancel <id>",
		Short: "Dismiss an open ask_user_question — agent receives a tool error",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.AgentQuestionsCancelInput](raw)
				if err != nil {
					return err
				}
				return runAgentQuestionsCancel(*in)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <id> positional or --json")
			}
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid question id %q: %w", args[0], err)
			}
			return runAgentQuestionsCancel(inputs.AgentQuestionsCancelInput{ID: id})
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runAgentQuestionsCancel(in inputs.AgentQuestionsCancelInput) error {
	if in.ID == 0 {
		return fmt.Errorf("question id is required")
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	q, err := c.CancelSessionQuestion(context.Background(), in.ID, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(q)
	}
	return emit(q)
}

// parseQuestionStates parses the user-supplied states slice into
// the typed model.QuestionState set. An empty input defaults to
// `open` — the most useful filter for "is there anything for me
// to answer right now". Unknown values error.
func parseQuestionStates(raw []string) ([]model.QuestionState, error) {
	if len(raw) == 0 {
		return []model.QuestionState{model.QuestionOpen}, nil
	}
	out := make([]model.QuestionState, 0, len(raw))
	for _, s := range raw {
		st, err := model.ParseQuestionState(s)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}
