//go:build !js

package tui

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// syncView is the BACI-109 sync-repo registry surface — the TUI peer of
// the BACI-108 desktop / web Sync view. Read-only by design (setup +
// linking are follow-ups in BACI-111 / BACI-112): one cursor over a flat
// list rendered in three sections (global toggle, per-sync-repo cards,
// unsynced project residual).
//
// The data load mirrors the BACI-107 HTTP composition — same call to
// sync.BuildRegistry the GET /sync/repos handler now uses. A
// self-re-arming 30 s tick keeps the view fresh without surfacing a
// loud last-refresh chip; the user can force a reload with `r`.
type syncView struct {
	store *store.Store
	repo  *model.Repo
	actor string

	registry          *bsync.Registry
	backgroundEnabled bool
	err               error

	// rows is the flat list of UI rows: the toggle row at 0, then a
	// header + one row per member per sync repo, then a header + one
	// row per unsynced project. Rebuilt on every reload. The cursor
	// indexes into this slice.
	rows   []syncRow
	cursor int
}

// syncRowKind discriminates the renderable shapes the flat row list
// carries. Only kindToggle is interactive at the moment (the toggle row
// flips the global background-sync flag); the rest exist so j/k can
// step the cursor cleanly through both sections.
type syncRowKind int

const (
	syncRowToggle syncRowKind = iota
	syncRowSectionHeader
	syncRowRepoHeader
	syncRowRepoDetail
	syncRowRepoMember
	syncRowUnsyncedProject
	syncRowEmpty
)

// syncRow is one entry in the flat row list. The render path switches
// on Kind; data fields are populated only for the relevant kinds and
// otherwise left as zero values.
type syncRow struct {
	Kind syncRowKind
	// Text is the pre-formatted line content for header / detail rows.
	Text string
	// Repo points at the sync-repo this row belongs to (nil for global
	// / section headers).
	Repo *bsync.SyncRepoEntry
	// Member is the per-project entry for kindRepoMember rows.
	Member *bsync.MemberProjectEntry
	// Unsynced is the per-residual entry for kindUnsyncedProject rows.
	Unsynced *bsync.UnsyncedProjectEntry
}

// syncTabRefreshInterval is the cadence of the view-local reload tick.
// 30 s matches the Board tab's auto-refresh and is faster than the
// background sync runner's 5 min cadence, so registry state never lags
// the underlying data by more than one tick.
const syncTabRefreshInterval = 30 * time.Second

// syncTabRefreshTickMsg fires on the syncTabRefreshInterval cadence and
// is broadcast to every view by the shell — the syncView handler
// reload()s and re-arms the next tick. The other views ignore it.
type syncTabRefreshTickMsg time.Time

// syncTabRefreshTick returns the Cmd that fires the next tick. Mirrors
// the other view-local ticker constructors in tui.go.
func syncTabRefreshTick() tea.Cmd {
	return tea.Tick(syncTabRefreshInterval, func(t time.Time) tea.Msg {
		return syncTabRefreshTickMsg(t)
	})
}

func newSyncView(s *store.Store, repo *model.Repo, actor string) *syncView {
	v := &syncView{store: s, repo: repo, actor: actor}
	v.reload()
	return v
}

// reload rebuilds the registry view from BuildRegistry + the global
// background-sync flag, then re-derives the flat row list. Errors
// surface in `err` and leave the previous registry in place so a
// transient failure doesn't blank the screen.
func (v *syncView) reload() {
	// nil-logger fan-in: a sync.BuildRegistry log warning would be
	// useful for diagnostics but the TUI's own *slog.Logger isn't
	// threaded into the view today. Passing nil is correct and matches
	// the wasm stub.
	reg, err := bsync.BuildRegistry(v.store, (*slog.Logger)(nil))
	if err != nil {
		v.err = err
		return
	}
	v.err = nil
	v.registry = reg
	enabled, err := v.store.GetSyncBackgroundEnabled()
	if err != nil {
		v.err = err
		return
	}
	v.backgroundEnabled = enabled
	v.rebuildRows()
	if v.cursor >= len(v.rows) {
		v.cursor = max(0, len(v.rows)-1)
	}
}

// rebuildRows flattens the registry into the syncRow slice the cursor
// steps through. The toggle row is always at index 0; the rest is
// section headers + per-card / per-member / per-unsynced rows.
func (v *syncView) rebuildRows() {
	rows := []syncRow{{Kind: syncRowToggle}}
	if v.registry == nil {
		v.rows = rows
		return
	}
	// Sync repositories section.
	rows = append(rows, syncRow{Kind: syncRowSectionHeader, Text: "Sync repositories"})
	if len(v.registry.SyncRepos) == 0 {
		rows = append(rows, syncRow{Kind: syncRowEmpty, Text: "(none on this machine)"})
	}
	for i := range v.registry.SyncRepos {
		entry := &v.registry.SyncRepos[i]
		rows = append(rows, syncRow{Kind: syncRowRepoHeader, Repo: entry})
		// Detail rows are rendered alongside the header in View(); the
		// row list still emits them so the cursor doesn't skip "past"
		// the visible card height. One detail row per non-empty leaf
		// (remote URL, local path, last-synced / cloned, last error if
		// present) keeps the row count tight without rendering blanks.
		rows = append(rows, syncRow{Kind: syncRowRepoDetail, Repo: entry, Text: "remote"})
		rows = append(rows, syncRow{Kind: syncRowRepoDetail, Repo: entry, Text: "path"})
		rows = append(rows, syncRow{Kind: syncRowRepoDetail, Repo: entry, Text: "lastsync"})
		if entry.LastError != nil {
			rows = append(rows, syncRow{Kind: syncRowRepoDetail, Repo: entry, Text: "error"})
		}
		if len(entry.Members) == 0 {
			rows = append(rows, syncRow{Kind: syncRowEmpty, Text: "  (no project members on disk)"})
			continue
		}
		for j := range entry.Members {
			m := &entry.Members[j]
			rows = append(rows, syncRow{Kind: syncRowRepoMember, Repo: entry, Member: m})
		}
	}
	// Unsynced projects section.
	rows = append(rows, syncRow{Kind: syncRowSectionHeader, Text: "Unsynced projects"})
	if len(v.registry.UnsyncedProjects) == 0 {
		rows = append(rows, syncRow{Kind: syncRowEmpty, Text: "(none — every tracked project has a sync remote)"})
	}
	for i := range v.registry.UnsyncedProjects {
		u := &v.registry.UnsyncedProjects[i]
		rows = append(rows, syncRow{Kind: syncRowUnsyncedProject, Unsynced: u})
	}
	v.rows = rows
}

func (v *syncView) Init() tea.Cmd       { return syncTabRefreshTick() }
func (v *syncView) Status() string      { return "" }
func (v *syncView) HasOverlay() bool    { return false }
func (v *syncView) CloseOverlay()       {}
func (v *syncView) CapturesInput() bool { return false }
func (v *syncView) Breadcrumb() string  { return "" }

func (v *syncView) Help() string {
	return "j/k move · g/G top/bottom · t toggle background sync · r reload · q quit"
}

func (v *syncView) Update(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(syncTabRefreshTickMsg); ok {
		v.reload()
		return syncTabRefreshTick()
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "j", "down":
		if v.cursor < len(v.rows)-1 {
			v.cursor++
		}
	case "k", "up":
		if v.cursor > 0 {
			v.cursor--
		}
	case "g", "home":
		v.cursor = 0
	case "G", "end":
		if len(v.rows) > 0 {
			v.cursor = len(v.rows) - 1
		}
	case "r":
		v.reload()
	case "t":
		v.toggleBackgroundSync()
	}
	return nil
}

// toggleBackgroundSync flips sync.background_enabled and writes the
// same audit-row shape the HTTP handler's handleSyncPreferencesSet
// uses, so `bacio history --kind app_setting` returns a consistent
// stream regardless of which surface flipped the bit.
func (v *syncView) toggleBackgroundSync() {
	next := !v.backgroundEnabled
	if err := v.store.SetSyncBackgroundEnabled(next); err != nil {
		v.err = err
		return
	}
	v.backgroundEnabled = next
	recordTUIOp(v.store, model.HistoryEntry{
		Actor:       v.actor,
		Op:          "sync_pref.update",
		Kind:        "app_setting",
		TargetLabel: "sync.background_enabled",
		Details:     fmt.Sprintf("background_enabled=%t", next),
	})
}

func (v *syncView) View(width, height int) string {
	if width == 0 || height == 0 {
		return ""
	}
	innerWidth := width - 2
	if innerWidth < 40 {
		innerWidth = 40
	}
	innerHeight := height - 2
	if innerHeight < 5 {
		innerHeight = 5
	}
	box := lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Width(innerWidth).Height(innerHeight)

	if v.err != nil {
		return box.Render(errorStyle.Render(v.err.Error()))
	}

	titleBar := lipgloss.NewStyle().
		Bold(true).Foreground(lipgloss.Color("231")).Background(colHeaderFocus).
		Width(innerWidth).Padding(0, 1).
		Render(v.titleText())

	lines := renderSyncRows(v.rows, v.cursor, innerWidth, v.backgroundEnabled)
	body := strings.Join(lines, "\n")
	// Reserve 2 rows for the title bar (1) and the blank line under it (1).
	bodyHeight := innerHeight - 2
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	body = scrollSyncBody(body, v.cursor, lines, bodyHeight)
	content := lipgloss.JoinVertical(lipgloss.Left, titleBar, "", body)
	return box.Render(content)
}

func (v *syncView) titleText() string {
	if v.registry == nil {
		return "Sync · (loading)"
	}
	return fmt.Sprintf("Sync · %d repositor%s · %d unsynced project%s",
		len(v.registry.SyncRepos), plural(len(v.registry.SyncRepos), "y", "ies"),
		len(v.registry.UnsyncedProjects), plural(len(v.registry.UnsyncedProjects), "", "s"))
}

// plural picks singular vs plural endings without dragging in a
// dependency. Keeps the title bar readable for empty registries.
func plural(n int, singular, pluralEnd string) string {
	if n == 1 {
		return singular
	}
	return pluralEnd
}

// renderSyncRows turns the flat row slice into per-line strings.
// Selection background is applied to the current cursor row.
func renderSyncRows(rows []syncRow, cursor, innerWidth int, backgroundEnabled bool) []string {
	out := make([]string, 0, len(rows))
	for i, r := range rows {
		selected := i == cursor
		out = append(out, renderSyncRow(r, selected, innerWidth, backgroundEnabled))
	}
	return out
}

// renderSyncRow renders one row at the given innerWidth. The toggle row
// fills the full width with its checkbox + label; section headers are
// bold; repo headers carry the label + remote URL; detail / member /
// unsynced rows are nested with leading spaces.
func renderSyncRow(r syncRow, selected bool, innerWidth int, backgroundEnabled bool) string {
	rowStyle := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1)
	if selected {
		rowStyle = rowStyle.Background(cardSelectedBG).Foreground(lipgloss.Color("231"))
	}
	switch r.Kind {
	case syncRowToggle:
		checkbox := "[ ]"
		if backgroundEnabled {
			checkbox = "[x]"
		}
		hint := mutedStyle.Render("(t to toggle — 5 min runner cadence)")
		return rowStyle.Render(truncate(checkbox+" Background sync  "+hint, innerWidth-2))
	case syncRowSectionHeader:
		return rowStyle.Render(boldStyle.Render(truncate(r.Text, innerWidth-2)))
	case syncRowRepoHeader:
		if r.Repo == nil {
			return rowStyle.Render("")
		}
		// Label on the left, remote URL muted on the right side of the
		// row (truncated together so a wide URL doesn't push the label
		// off-screen).
		label := boldStyle.Render(r.Repo.Label)
		remote := mutedStyle.Render(truncate(r.Repo.RemoteURL, max(8, innerWidth-2-lipgloss.Width(label)-2)))
		line := label + "  " + remote
		return rowStyle.Render(truncate(line, innerWidth-2))
	case syncRowRepoDetail:
		if r.Repo == nil {
			return rowStyle.Render("")
		}
		var text string
		switch r.Text {
		case "remote":
			// remote is already on the header; show the local path here
			// to give the user the "where is this on disk" answer
			// without duplication.
			text = mutedStyle.Render("path: ") + truncate(r.Repo.LocalPath, max(8, innerWidth-12))
		case "path":
			// "cloned" timestamp — formatted relative so the user can
			// see "cloned 3d ago".
			text = mutedStyle.Render("cloned: ") + relAgo(time.Since(r.Repo.ClonedAt)) + mutedStyle.Render(" ago")
		case "lastsync":
			if r.Repo.LastSyncAt == nil {
				text = mutedStyle.Render("last sync: never")
			} else {
				text = mutedStyle.Render("last sync: ") + relAgo(time.Since(*r.Repo.LastSyncAt)) + mutedStyle.Render(" ago")
			}
		case "error":
			if r.Repo.LastError != nil {
				text = errorStyle.Render("error: " + truncate(*r.Repo.LastError, max(8, innerWidth-12)))
			}
		}
		return rowStyle.Render("  " + text)
	case syncRowRepoMember:
		if r.Member == nil {
			return rowStyle.Render("")
		}
		key := keyStyle.Render(r.Member.Prefix)
		name := r.Member.Name
		if name == "" {
			name = mutedStyle.Render("(no local name)")
		}
		status := renderMemberStatus(r.Member.Status)
		line := "  " + key + "  " + name + "  " + status
		return rowStyle.Render(truncate(line, innerWidth-2))
	case syncRowUnsyncedProject:
		if r.Unsynced == nil {
			return rowStyle.Render("")
		}
		key := keyStyle.Render(r.Unsynced.Prefix)
		line := "  " + key + "  " + r.Unsynced.Name + "  " + mutedStyle.Render(truncate(r.Unsynced.Path, max(8, innerWidth-2-12-lipgloss.Width(r.Unsynced.Name))))
		return rowStyle.Render(truncate(line, innerWidth-2))
	case syncRowEmpty:
		return rowStyle.Render("  " + mutedStyle.Render(truncate(r.Text, innerWidth-4)))
	}
	return rowStyle.Render("")
}

// renderMemberStatus paints a small status chip per membership kind.
// linked = green, phantom = amber (matches the "waiting" amber on the
// kanban), absent = muted grey.
func renderMemberStatus(s bsync.MembershipStatus) string {
	switch s {
	case bsync.StatusLinked:
		return lipgloss.NewStyle().
			Background(lipgloss.Color("76")).Foreground(lipgloss.Color("231")).
			Padding(0, 1).Render("linked")
	case bsync.StatusPhantom:
		return lipgloss.NewStyle().
			Background(takenColor).Foreground(lipgloss.Color("232")).
			Padding(0, 1).Render("phantom")
	case bsync.StatusAbsent:
		return mutedStyle.Render("[absent]")
	}
	return mutedStyle.Render(string(s))
}

// scrollSyncBody trims the rendered rows to the visible window so the
// cursor stays in view. The body height excludes the title bar + the
// blank line between it and the rows (those are joined separately in
// View).
func scrollSyncBody(body string, cursor int, rows []string, height int) string {
	if height <= 0 || len(rows) == 0 {
		return body
	}
	scroll := 0
	if cursor < scroll {
		scroll = cursor
	}
	if cursor >= scroll+height {
		scroll = cursor - height + 1
	}
	maxScroll := len(rows) - height
	if maxScroll < 0 {
		maxScroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	end := scroll + height
	if end > len(rows) {
		end = len(rows)
	}
	return strings.Join(rows[scroll:end], "\n")
}
