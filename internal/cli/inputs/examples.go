package inputs

func strPtr(s string) *string { return &s }

// Examples are realistic, hand-curated payloads attached to each input
// schema's `examples` field, so an LLM consumer can adapt a working call
// rather than improvise from the type signature alone.
var (
	ExampleIssueAdd = IssueAddInput{
		Title:       "Pin tab strip in place",
		FeatureSlug: "tui-polish",
		Description: "Body height should clip the tab strip so it doesn't drift on overflow.",
		State:       "todo",
		Tags:        []string{"ui", "tui"},
	}
	ExampleIssueEdit = IssueEditInput{
		Key:   "MINI-42",
		Title: strPtr("Pin tab strip with body-height clipping"),
	}
	ExampleIssueState = IssueStateInput{
		Key:   "MINI-42",
		State: "in_progress",
	}
	ExampleIssueAssign = IssueAssignInput{
		Key:      "MINI-42",
		Assignee: "agent-alice",
	}
	ExampleIssueUnassign = IssueUnassignInput{
		Key: "MINI-42",
	}
	ExampleIssueNext = IssueNextInput{
		FeatureSlug: "tui-polish",
	}
	ExampleIssueRm = IssueRmInput{
		Key: "MINI-99",
	}

	ExampleFeatureAdd = FeatureAddInput{
		Title:       "Auth rewrite",
		Slug:        "auth",
		Description: "Replace the legacy session-token middleware to meet new compliance rules.",
	}
	ExampleFeatureEdit = FeatureEditInput{
		Slug:        "auth",
		Description: strPtr("Compliance-driven session-token rewrite (legal flagged the old impl)."),
	}
	ExampleFeatureRm = FeatureRmInput{
		Slug: "auth-old",
	}

	ExampleCommentAdd = CommentAddInput{
		IssueKey: "MINI-42",
		Author:   "agent-alice",
		Body:     "agent is editing the wrong file — see comment thread on PR 123",
		Eval:     true,
		// BACI-141: optional anchor pinning the note to a specific event
		// inside a `.jsonl` transcript (`tool_use_id:<id>` or
		// `line_index:<n>`). Omit on board-level eval notes — they
		// surface pinned to the dispatch prompt card.
		TranscriptEventRef: "tool_use_id:toolu_01ABCDEFGhij",
	}
	ExampleCommentRm = CommentRmInput{
		IssueKey:    "MINI-42",
		CommentUUID: "019e4d42-15ab-7daf-b65d-c576164691db",
	}

	ExampleFeatureCommentAdd = FeatureCommentAddInput{
		FeatureSlug: "auth",
		Author:      "agent-alice",
		Body:        "## MINI-42 handoff\n\n**Files of context.** internal/auth/session.go.\n**Deviations from plan.** None.\n**Work not done.** Cookie scoping deferred to MINI-43.",
	}
	ExampleFeatureCommentRm = FeatureCommentRmInput{
		FeatureSlug: "auth",
		CommentUUID: "019e4d42-15ab-7daf-b65d-c576164691db",
	}

	ExampleLink = LinkInput{
		From: "MINI-42",
		Type: "blocks",
		To:   "MINI-7",
	}
	ExampleUnlink = UnlinkInput{
		A: "MINI-42",
		B: "MINI-7",
	}

	ExamplePRAttach = PRAttachInput{
		IssueKey: "MINI-42",
		URL:      "https://github.com/example/bacio/pull/123",
	}
	ExamplePRDetach = PRDetachInput{
		IssueKey: "MINI-42",
		URL:      "https://github.com/example/bacio/pull/123",
	}

	ExampleTagAdd = TagAddInput{
		IssueKey: "MINI-42",
		Tags:     []string{"ui", "tui"},
	}
	ExampleTagRm = TagRmInput{
		IssueKey: "MINI-42",
		Tags:     []string{"wip"},
	}

	ExampleDocAdd = DocAddInput{
		Filename: "auth-design.md",
		Type:     "architecture",
		Content:  "# Auth design\n\nMotivation, options, and the chosen path.\n",
	}
	ExampleDocEdit = DocEditInput{
		Filename: "auth-design.md",
		Type:     strPtr("designs"),
	}
	ExampleDocRename = DocRenameInput{
		OldFilename: "auth.md",
		NewFilename: "auth-design.md",
		Type:        "architecture",
	}
	ExampleDocRm = DocRmInput{
		Filename: "auth-old.md",
	}
	ExampleDocLink = DocLinkInput{
		Filename:    "auth-design.md",
		IssueKey:    "MINI-42",
		Description: "Source-of-truth design doc.",
	}
	ExampleDocUnlink = DocUnlinkInput{
		Filename:    "auth-design.md",
		FeatureSlug: "auth",
	}
	ExampleDocExport = DocExportInput{
		Filename: "auth-design.md",
		To:       "docs/auth-design.md",
	}

	ExampleAgentRegister = AgentRegisterInput{
		SessionID:   "092d8907-a5ed-48cf-9fdd-22c3941f3710",
		Actor:       "agent-claude",
		Agent:       "cheerful-otter@claude.shiny",
		NewIdentity: true,
		Model:       "claude-sonnet-4-6",
		Host:        "shiny.local",
		Branch:      "feat/auth-rewrite",
	}
	ExampleAgentHeartbeat = AgentHeartbeatInput{
		SessionID: "092d8907-a5ed-48cf-9fdd-22c3941f3710",
		Model:     "claude-opus-4-7",
		Branch:    "feat/auth-rewrite",
	}
	ExampleAgentEnd = AgentEndInput{
		SessionID:     "092d8907-a5ed-48cf-9fdd-22c3941f3710",
		Reason:        "stop",
		StateOnOrphan: "in_progress",
	}
	ExampleAgentClaim = AgentClaimInput{
		SessionID: "092d8907-a5ed-48cf-9fdd-22c3941f3710",
		IssueKey:  "MINI-42",
		Prompt:    "Implement the tab-strip pinning fix end-to-end, then open a PR.",
	}
	ExampleAgentRelease = AgentReleaseInput{
		SessionID:  "092d8907-a5ed-48cf-9fdd-22c3941f3710",
		IssueKey:   "MINI-42",
		FinalState: "in_review",
	}
	ExampleAgentDispatch = AgentDispatchInput{
		TargetAgent: "swift-otter@claude.shiny",
		IssueKey:    "MINI-42",
		Message:     "This regressed after the tab-strip change — please pick it up before EOD.",
	}
	ExampleAgentAck = AgentAckInput{
		ID:   7,
		Note: "On it — claimed MINI-42, opening a PR shortly.",
	}
	ExampleAgentCancel = AgentCancelInput{
		ID: 7,
	}
	ExampleIssueDispatch = IssueDispatchInput{
		Mode: "implement",
	}

	ExampleAgentQuestionsList = AgentQuestionsListInput{
		SessionID: "092d8907-a5ed-48cf-9fdd-22c3941f3710",
		States:    []string{"open"},
	}
	ExampleAgentQuestionsShow = AgentQuestionsShowInput{
		ID: 1,
	}
	ExampleAgentQuestionsAnswer = AgentQuestionsAnswerInput{
		ID: 1,
		Answers: map[string]any{
			"Which approach should I take?": "Option A",
		},
	}
	ExampleAgentQuestionsCancel = AgentQuestionsCancelInput{
		ID: 1,
	}

	ExampleRepoCreate = RepoCreateInput{
		Prefix:    "MINI",
		Name:      "bacio",
		Path:      "/Users/dev/code/bacio",
		RemoteURL: "https://github.com/example/bacio.git",
	}
	ExampleRepoRm = RepoRmInput{
		Prefix:  "MINI",
		Confirm: "MINI",
	}
	ExampleRepoLink = RepoLinkInput{
		Prefix: "MINI",
		Path:   "/Users/dev/code/mini",
	}

	ExampleSettingsTemplateSet = SettingsTemplateSetInput{
		Slug: "review",
		Body: "Review {{issue_id}} ({{issue_title}}): check correctness, tests, and the acceptance criteria. Report findings — don't change code.",
	}
	ExampleSettingsTemplateReset = SettingsTemplateResetInput{
		Slug: "review",
	}

	ExampleSettingsTemplateStatesSet = SettingsTemplateStatesSetInput{
		Slug:   "review",
		States: []string{"in_review"},
	}
	ExampleSettingsTemplateStatesReset = SettingsTemplateStatesResetInput{
		Slug: "review",
	}

	ExampleSettingsTemplateAdd = SettingsTemplateAddInput{
		Slug:             "spike",
		Name:             "Spike",
		Body:             "Spike on {{issue_id}} ({{issue_title}}) — produce a short investigation note, no code.",
		States:           []string{"todo"},
		ConcurrencyLimit: 0,
		ActionLabel:      "Spike",
	}
	ExampleSettingsTemplateSetConcurrency = SettingsTemplateSetConcurrencyInput{
		Slug:             "ship",
		ConcurrencyLimit: 1,
	}
	ExampleSettingsTemplateSetActionLabel = SettingsTemplateSetActionLabelInput{
		Slug:        "spike",
		ActionLabel: "Investigate",
	}
	ExampleSettingsTemplateRename = SettingsTemplateRenameInput{
		Slug:    "spike",
		NewSlug: "investigation",
		NewName: "Investigation",
	}
	ExampleSettingsTemplateRm = SettingsTemplateRmInput{
		Slug: "spike",
	}
	ExampleSettingsTemplateRestoreDefaults = SettingsTemplateRestoreDefaultsInput{}

	ExampleWorktreeInit = WorktreeInitInput{
		Slug: "bacio-baci-63",
		Port: 5321,
	}
	ExampleWorktreeRm = WorktreeRmInput{
		Confirm: "bacio-baci-63",
		PurgeDB: false,
	}

	ExampleIssueArchive       = IssueArchiveInput{Key: "MINI-42"}
	ExampleIssueUnarchive     = IssueUnarchiveInput{Key: "MINI-42"}
	ExampleFeatureArchive     = FeatureArchiveInput{Slug: "auth-old"}
	ExampleFeatureUnarchive   = FeatureUnarchiveInput{Slug: "auth-old"}
	ExampleDocArchive         = DocArchiveInput{Filename: "auth-old.md"}
	ExampleDocUnarchive       = DocUnarchiveInput{Filename: "auth-old.md"}
	ExampleArchiveSweep       = ArchiveSweepInput{}
	ExampleSettingsShowArchived = SettingsShowArchivedInput{Value: true}
	ExampleSettingsSyncBackground = SettingsSyncBackgroundInput{Value: false}
	ExampleSettingsArchive        = SettingsArchiveInput{
		AutoEnabled:   true,
		RetentionDays: 7,
	}

	// SyncSetup (BACI-110) — three representative payloads, one per mode.
	// The registry only attaches one example to the schema, but the
	// other two are kept available for tests and future doc generators.
	ExampleSyncSetupClone = SyncSetupInput{
		Mode:   "clone",
		Remote: "git@example.com:bacio/team-sync.git",
	}
	ExampleSyncSetupInit = SyncSetupInput{
		Mode:      "init",
		Remote:    "git@example.com:bacio/team-sync.git",
		LocalPath: "/Users/dev/.bacio/sync/team-sync",
	}
	ExampleSyncSetupAttach = SyncSetupInput{
		Mode:   "attach",
		Remote: "git@example.com:bacio/team-sync.git",
	}
)
