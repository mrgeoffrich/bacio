package main

import (
	"context"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/version"
)

// PromptTemplateDTO is one editable dispatch prompt template, shaped
// for the desktop Settings panel. Slug is the storage / CLI identifier;
// Mode is kept as an alias of Slug for backward-compat with frontend
// code that still keys by `mode`. Body is the persisted body. Default
// is the built-in embedded default for the slug (empty for user-created
// templates); IsDefault reports whether Body still matches it.
// AllowedStates is the state-gate; DefaultStates is the built-in
// default for the slug (empty for user-created); StatesAreDefault
// reports whether the gate still matches.
type PromptTemplateDTO struct {
	Slug                    string   `json:"slug"`
	Mode                    string   `json:"mode"`
	Label                   string   `json:"label"`
	Body                    string   `json:"body"`
	Default                 string   `json:"default"`
	IsBuiltin               bool     `json:"isBuiltin"`
	IsDefault               bool     `json:"isDefault"`
	AllowedStates           []string `json:"allowedStates"`
	DefaultStates           []string `json:"defaultStates"`
	StatesAreDefault        bool     `json:"statesAreDefault"`
	ConcurrencyLimit        int      `json:"concurrencyLimit"`
	DefaultConcurrencyLimit int      `json:"defaultConcurrencyLimit"`
	ConcurrencyIsDefault    bool     `json:"concurrencyIsDefault"`
	// BACI-67: imperative override rendered on the dispatch action
	// menus. ActionLabel is the persisted value (empty = derive from
	// Name via the gerund→imperative rule); DefaultActionLabel is the
	// built-in imperative seed for the slug (empty for user-created
	// templates); ActionLabelIsDefault reports whether the persisted
	// override still matches the built-in default — drives the
	// Settings panel's "reset" affordance.
	ActionLabel          string `json:"actionLabel"`
	DefaultActionLabel   string `json:"defaultActionLabel"`
	ActionLabelIsDefault bool   `json:"actionLabelIsDefault"`
}

// SettingsService is the Wails-bound API for the desktop Settings panel.
// It owns the customisable dispatch prompt templates (now arbitrary in
// count and shape — see BACI-31) and the Board UI preferences.
type SettingsService struct {
	client client.Client
}

func NewSettingsService(c client.Client) *SettingsService {
	return &SettingsService{client: c}
}

// PromptPlaceholders returns the placeholder tokens a template body can
// interpolate (e.g. "issue_id"), so the Settings UI can show them next
// to the editors. The tokens are wrapped in {{...}} when substituted.
func (s *SettingsService) PromptPlaceholders() []string {
	return append([]string{}, model.PromptTemplateTokens...)
}

// BacioVersion returns the version string of the bacio binary the
// desktop app is currently running. Surfaced on the Settings panel so
// you can cross-check what the desktop client is running against the
// per-session "Bacio version" the Agents panel shows.
func (s *SettingsService) BacioVersion() string {
	return version.String()
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func statesToStrings(states []model.State) []string {
	out := make([]string, len(states))
	for i, st := range states {
		out[i] = string(st)
	}
	return out
}

// dtoForTemplate maps a store row into the DTO shape the desktop
// consumes.
func dtoForTemplate(t *store.PromptTemplate) PromptTemplateDTO {
	def := model.DefaultPromptBodyForBuiltinSlug(t.Slug)
	defStates := statesToStrings(model.DefaultPromptStatesForBuiltinSlug(t.Slug))
	allowed := statesToStrings(t.AllowedStates)
	label := t.Name
	if label == "" {
		label = model.BuiltinTemplateLabel(t.Slug)
		if label == "" {
			label = t.Slug
		}
	}
	defConc := model.DefaultConcurrencyLimit(t.Slug)
	defAction := model.BuiltinTemplateActionLabel(t.Slug)
	return PromptTemplateDTO{
		Slug:                    t.Slug,
		Mode:                    t.Slug,
		Label:                   label,
		Body:                    t.Body,
		Default:                 def,
		IsBuiltin:               t.IsBuiltin,
		IsDefault:               t.IsBuiltin && t.Body == def,
		AllowedStates:           allowed,
		DefaultStates:           defStates,
		StatesAreDefault:        t.IsBuiltin && sameStrings(allowed, defStates),
		ConcurrencyLimit:        t.ConcurrencyLimit,
		DefaultConcurrencyLimit: defConc,
		ConcurrencyIsDefault:    t.IsBuiltin && t.ConcurrencyLimit == defConc,
		ActionLabel:             t.ActionLabel,
		DefaultActionLabel:      defAction,
		ActionLabelIsDefault:    t.IsBuiltin && t.ActionLabel == defAction,
	}
}

// ListPromptTemplates returns every registered template in store
// iteration order — the desktop Settings panel renders them in this
// order and the per-card action menu in the Board iterates the same
// list filtered by state-gate.
func (s *SettingsService) ListPromptTemplates() ([]PromptTemplateDTO, error) {
	ctx := context.Background()
	tmpls, err := s.client.ListPromptTemplates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PromptTemplateDTO, 0, len(tmpls))
	for _, t := range tmpls {
		out = append(out, dtoForTemplate(t))
	}
	return out, nil
}

// SavePromptTemplate stores a new body for one template slug. An empty
// body reverts a built-in to its embedded default (and is a no-op
// edit for a user-created template).
func (s *SettingsService) SavePromptTemplate(slug, body string) (PromptTemplateDTO, error) {
	ctx := context.Background()
	if err := s.client.SetPromptTemplate(ctx, slug, body, false); err != nil {
		return PromptTemplateDTO{}, err
	}
	return s.refreshedDTO(ctx, slug)
}

// SavePromptStates stores a new state-gate for one template. An empty
// slice reverts a built-in to its embedded default gate.
func (s *SettingsService) SavePromptStates(slug string, states []string) (PromptTemplateDTO, error) {
	ctx := context.Background()
	if err := s.client.SetPromptStates(ctx, slug, states, false); err != nil {
		return PromptTemplateDTO{}, err
	}
	return s.refreshedDTO(ctx, slug)
}

// SavePromptConcurrency (BACI-51) sets a template's per-(repo, slug)
// in-flight dispatch cap the matcher enforces. 0 = unlimited.
func (s *SettingsService) SavePromptConcurrency(slug string, concurrencyLimit int) (PromptTemplateDTO, error) {
	ctx := context.Background()
	if _, err := s.client.SetPromptTemplateConcurrencyLimit(ctx, inputs.SettingsTemplateSetConcurrencyInput{
		Slug:             slug,
		ConcurrencyLimit: concurrencyLimit,
	}, false); err != nil {
		return PromptTemplateDTO{}, err
	}
	return s.refreshedDTO(ctx, slug)
}

// AddPromptTemplate creates a brand-new template. actionLabel is the
// BACI-67 imperative override rendered on the dispatch action menus;
// pass "" to skip the override (the UI derives one from name).
func (s *SettingsService) AddPromptTemplate(slug, name, body string, states []string, actionLabel string) (PromptTemplateDTO, error) {
	ctx := context.Background()
	t, err := s.client.AddPromptTemplate(ctx, inputs.SettingsTemplateAddInput{
		Slug:        slug,
		Name:        name,
		Body:        body,
		States:      states,
		ActionLabel: actionLabel,
	}, false)
	if err != nil {
		return PromptTemplateDTO{}, err
	}
	return dtoForTemplate(t), nil
}

// SavePromptActionLabel (BACI-67) updates a template's imperative
// override — the verb the dispatch action menus render. An empty
// actionLabel clears the override (the UI then derives from Name via
// the gerund→imperative rule).
func (s *SettingsService) SavePromptActionLabel(slug, actionLabel string) (PromptTemplateDTO, error) {
	ctx := context.Background()
	if _, err := s.client.SetPromptTemplateActionLabel(ctx, inputs.SettingsTemplateSetActionLabelInput{
		Slug:        slug,
		ActionLabel: actionLabel,
	}, false); err != nil {
		return PromptTemplateDTO{}, err
	}
	return s.refreshedDTO(ctx, slug)
}

// RenamePromptTemplate renames an existing template — either the slug,
// the display name, or both.
func (s *SettingsService) RenamePromptTemplate(slug, newSlug, newName string) (PromptTemplateDTO, error) {
	ctx := context.Background()
	t, err := s.client.RenamePromptTemplate(ctx, inputs.SettingsTemplateRenameInput{
		Slug:    slug,
		NewSlug: newSlug,
		NewName: newName,
	}, false)
	if err != nil {
		return PromptTemplateDTO{}, err
	}
	return dtoForTemplate(t), nil
}

// DeletePromptTemplate removes a template by slug. The historical
// dispatches that reference the slug are left intact (a dispatch is a
// snapshot, not a live foreign key) — use RestoreBuiltinPromptTemplates
// to re-seed a deleted built-in.
func (s *SettingsService) DeletePromptTemplate(slug string) (PromptTemplateDTO, error) {
	ctx := context.Background()
	t, err := s.client.DeletePromptTemplate(ctx, inputs.SettingsTemplateRmInput{Slug: slug}, false)
	if err != nil {
		return PromptTemplateDTO{}, err
	}
	return dtoForTemplate(t), nil
}

// RestoreBuiltinPromptTemplates re-seeds any built-in slug that doesn't
// currently have a row from the embedded defaults. Idempotent. Returns
// the refreshed full template list so the frontend can update its
// `templates` state in one shot.
func (s *SettingsService) RestoreBuiltinPromptTemplates() ([]PromptTemplateDTO, error) {
	ctx := context.Background()
	if _, err := s.client.RestoreBuiltinPromptTemplates(ctx, false); err != nil {
		return nil, err
	}
	return s.ListPromptTemplates()
}

// refreshedDTO re-reads one template by slug and builds its DTO — the
// shared tail of SavePromptTemplate / SavePromptStates.
func (s *SettingsService) refreshedDTO(ctx context.Context, slug string) (PromptTemplateDTO, error) {
	t, err := s.client.GetPromptTemplate(ctx, slug)
	if err != nil {
		return PromptTemplateDTO{}, err
	}
	return dtoForTemplate(t), nil
}

// BoardPreferencesDTO is the desktop Board's UI preferences, shaped for
// the Settings panel. HideEmptyColumns drops kanban columns with zero
// cards from the Board.
type BoardPreferencesDTO struct {
	HideEmptyColumns bool `json:"hideEmptyColumns"`
}

// GetBoardPreferences returns the persisted desktop Board UI
// preferences (or the built-in defaults when none are stored).
func (s *SettingsService) GetBoardPreferences() (BoardPreferencesDTO, error) {
	prefs, err := s.client.GetBoardPreferences(context.Background())
	if err != nil {
		return BoardPreferencesDTO{}, err
	}
	return BoardPreferencesDTO{HideEmptyColumns: prefs.HideEmptyColumns}, nil
}

// SetBoardPreferences stores the desktop Board's hide-empty-columns
// preference and returns the refreshed DTO.
func (s *SettingsService) SetBoardPreferences(hideEmptyColumns bool) (BoardPreferencesDTO, error) {
	ctx := context.Background()
	if err := s.client.SetBoardPreferences(ctx, client.BoardPreferences{
		HideEmptyColumns: hideEmptyColumns,
	}, false); err != nil {
		return BoardPreferencesDTO{}, err
	}
	return s.GetBoardPreferences()
}

// DisplayPreferencesDTO is the BACI-68 display.show_archived global
// toggle, shaped for the desktop Settings panel.
type DisplayPreferencesDTO struct {
	ShowArchived bool `json:"showArchived"`
}

// GetDisplayPreferences returns the current display.show_archived
// value (BACI-68). The desktop Board / Docs / Features views consult
// this on every refresh — when on, archived rows surface as visibly-
// muted cards; when off they're hidden entirely.
func (s *SettingsService) GetDisplayPreferences() (DisplayPreferencesDTO, error) {
	v, err := s.client.GetDisplayShowArchived(context.Background())
	if err != nil {
		return DisplayPreferencesDTO{}, err
	}
	return DisplayPreferencesDTO{ShowArchived: v}, nil
}

// SetDisplayPreferences writes display.show_archived and returns the
// refreshed DTO (BACI-68).
func (s *SettingsService) SetDisplayPreferences(showArchived bool) (DisplayPreferencesDTO, error) {
	v, err := s.client.SetDisplayShowArchived(context.Background(), showArchived, false)
	if err != nil {
		return DisplayPreferencesDTO{}, err
	}
	return DisplayPreferencesDTO{ShowArchived: v}, nil
}

// SyncPreferencesDTO is the BACI-89 background-sync toggle shaped for
// the desktop Sync view. Mirrors BoardPreferencesDTO / DisplayPreferencesDTO.
type SyncPreferencesDTO struct {
	BackgroundEnabled bool `json:"backgroundEnabled"`
}

// GetSyncPreferences returns the current sync.background_enabled
// value. Backed by the same client.GetSyncBackgroundEnabled path the
// CLI and HTTP surfaces use.
func (s *SettingsService) GetSyncPreferences() (SyncPreferencesDTO, error) {
	v, err := s.client.GetSyncBackgroundEnabled(context.Background())
	if err != nil {
		return SyncPreferencesDTO{}, err
	}
	return SyncPreferencesDTO{BackgroundEnabled: v}, nil
}

// SetSyncPreferences flips sync.background_enabled and returns the
// refreshed DTO. The client records the audit row.
func (s *SettingsService) SetSyncPreferences(backgroundEnabled bool) (SyncPreferencesDTO, error) {
	v, err := s.client.SetSyncBackgroundEnabled(context.Background(), backgroundEnabled, false)
	if err != nil {
		return SyncPreferencesDTO{}, err
	}
	return SyncPreferencesDTO{BackgroundEnabled: v}, nil
}

// SyncRegistryDTO is the BACI-108 standalone Sync view payload —
// camelCase mirror of client.SyncRegistry. Re-shaped here (rather
// than carried verbatim) so the Wails seam follows the convention
// every other DTO in this file uses (camelCase JSON tags for the
// generated TS bindings).
type SyncRegistryDTO struct {
	SyncRepos        []SyncRepoDTO        `json:"syncRepos"`
	UnsyncedProjects []UnsyncedProjectDTO `json:"unsyncedProjects"`
}

// SyncRepoDTO is one entry in the registry.
type SyncRepoDTO struct {
	RemoteURL  string             `json:"remoteUrl"`
	Label      string             `json:"label"`
	LocalPath  string             `json:"localPath"`
	ClonedAt   string             `json:"clonedAt"`
	LastSyncAt string             `json:"lastSyncAt,omitempty"`
	LastError  string             `json:"lastError,omitempty"`
	InProgress bool               `json:"inProgress"`
	Projects   []MemberProjectDTO `json:"projects"`
}

// MemberProjectDTO mirrors client.MemberProject.
type MemberProjectDTO struct {
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
	UUID   string `json:"uuid,omitempty"`
	Status string `json:"status"`
}

// UnsyncedProjectDTO mirrors client.UnsyncedProject.
type UnsyncedProjectDTO struct {
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
	UUID   string `json:"uuid"`
	Path   string `json:"path"`
}

// GetSyncRegistry returns the registry of sync repos this machine
// knows plus the tracked project repos that aren't yet attached. Backs
// the BACI-108 standalone Sync view on desktop. The same payload ships
// over the wire on `bacio web` via GET /sync/repos.
func (s *SettingsService) GetSyncRegistry() (SyncRegistryDTO, error) {
	reg, err := s.client.SyncRegistry(context.Background())
	if err != nil {
		return SyncRegistryDTO{}, err
	}
	out := SyncRegistryDTO{
		SyncRepos:        make([]SyncRepoDTO, 0, len(reg.SyncRepos)),
		UnsyncedProjects: make([]UnsyncedProjectDTO, 0, len(reg.UnsyncedProjects)),
	}
	for _, r := range reg.SyncRepos {
		dto := SyncRepoDTO{
			RemoteURL:  r.RemoteURL,
			Label:      r.Label,
			LocalPath:  r.LocalPath,
			ClonedAt:   r.ClonedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			InProgress: r.InProgress,
			Projects:   make([]MemberProjectDTO, 0, len(r.Projects)),
		}
		if r.LastSyncAt != nil {
			dto.LastSyncAt = r.LastSyncAt.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		if r.LastError != nil {
			dto.LastError = *r.LastError
		}
		for _, p := range r.Projects {
			dto.Projects = append(dto.Projects, MemberProjectDTO{
				Prefix: p.Prefix,
				Name:   p.Name,
				UUID:   p.UUID,
				Status: p.Status,
			})
		}
		out.SyncRepos = append(out.SyncRepos, dto)
	}
	for _, u := range reg.UnsyncedProjects {
		out.UnsyncedProjects = append(out.UnsyncedProjects, UnsyncedProjectDTO{
			Prefix: u.Prefix,
			Name:   u.Name,
			UUID:   u.UUID,
			Path:   u.Path,
		})
	}
	return out, nil
}

// RepoLinkResultDTO mirrors client.RepoLinkResult — the success payload
// returned to the desktop / web Sync view's Link-local modal after a
// phantom row is bound to a working tree.
type RepoLinkResultDTO struct {
	Repo          RepoLinkResultRepoDTO `json:"repo"`
	SyncRemoteURL string                `json:"syncRemoteUrl"`
	AlreadyLinked bool                  `json:"alreadyLinked"`
}

// RepoLinkResultRepoDTO is the subset of model.Repo the modal needs
// to render success — prefix + path are enough to confirm to the
// user which working tree it landed on. Keeping a separate DTO (rather
// than embedding model.Repo) keeps the camel-case JSON tag convention
// consistent with the rest of this file.
type RepoLinkResultRepoDTO struct {
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	UUID   string `json:"uuid"`
}

// LinkPhantomRepo (BACI-112) binds a phantom repo row to a local git
// working tree. Wired to the desktop SyncView's PhantomLinkModal and
// — via api.ts shim — to the web SyncView. The dry-run path is not
// exposed here; the modal asks for confirmation before submitting and
// the underlying write is sub-millisecond, so a separate rehearse
// surface would be more noise than signal. A future caller that
// needs it can pass `dryRun=true` to the underlying client method
// directly.
func (s *SettingsService) LinkPhantomRepo(prefix, path string) (RepoLinkResultDTO, error) {
	result, err := s.client.LinkPhantomRepo(context.Background(), prefix, path, false)
	if err != nil {
		return RepoLinkResultDTO{}, err
	}
	dto := RepoLinkResultDTO{
		SyncRemoteURL: result.SyncRemoteURL,
		AlreadyLinked: result.AlreadyLinked,
	}
	if result.Repo != nil {
		dto.Repo = RepoLinkResultRepoDTO{
			Prefix: result.Repo.Prefix,
			Name:   result.Repo.Name,
			Path:   result.Repo.Path,
			UUID:   result.Repo.UUID,
		}
	}
	return dto, nil
}
