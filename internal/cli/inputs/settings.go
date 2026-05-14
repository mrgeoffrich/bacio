package inputs

// SettingsTemplateSetInput is the payload for
// `bacio settings template set --json`. Mode is a dispatch stage
// (plan, implement, review, ship, fix_review). Body is the custom
// template text; the {{issue_id}}, {{issue_title}} and {{repo_prefix}}
// placeholders are substituted with the issue's context at dispatch
// time. An empty Body is rejected here — use `settings template reset`
// to revert a stage to its built-in default.
type SettingsTemplateSetInput struct {
	Mode string `json:"mode"`
	Body string `json:"body"`
}

// SettingsTemplateResetInput is the payload for
// `bacio settings template reset --json`. It clears the custom override
// for Mode, reverting that stage to its built-in default template.
type SettingsTemplateResetInput struct {
	Mode string `json:"mode"`
}
