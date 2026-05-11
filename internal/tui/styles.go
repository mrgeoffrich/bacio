package tui

import (
	"hash/fnv"

	"github.com/charmbracelet/lipgloss"
)

// Shared styles. Kept in one place so palette tweaks land everywhere.
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("57")).
			Padding(0, 1)
	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Padding(0, 1)
	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Padding(0, 1)
	mutedStyle = lipgloss.NewStyle().Foreground(mutedColor)
	keyStyle   = lipgloss.NewStyle().Foreground(cardKeyColor)
	boldStyle  = lipgloss.NewStyle().Bold(true)

	tabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("57")).
			Padding(0, 2)
	tabInactive = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Padding(0, 2)
	tabSep = lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render("·")
)

// repoGlyph prefixes the repo name in the header. Defaults to the Powerline
// / Nerd Font git-branch glyph (U+E0A0) since most dev terminals carry it.
// If your terminal renders a tofu box, swap to a simpler choice like "⎇" or
// "git:".
const repoGlyph = ""

var (
	colBorder        = lipgloss.RoundedBorder()
	colBorderColor   = lipgloss.Color("240")
	colFocusBorder   = lipgloss.Color("212")
	colHeaderFocus   = lipgloss.Color("212")
	colHeaderUnfocus = lipgloss.Color("240")
	cardSelectedBG   = lipgloss.Color("57")
	cardKeyColor     = lipgloss.Color("147")
	mutedColor       = lipgloss.Color("241")
)

// featurePalette is a curated set of distinguishable 256-colour ANSI
// values used to colour the per-card feature stripe. Picked for
// reasonable contrast on dark terminals; avoid greys / borderline whites
// since they collide with chrome colours above.
var featurePalette = []lipgloss.Color{
	lipgloss.Color("39"),  // bright blue
	lipgloss.Color("208"), // orange
	lipgloss.Color("76"),  // green
	lipgloss.Color("203"), // pink
	lipgloss.Color("220"), // gold
	lipgloss.Color("75"),  // sky
	lipgloss.Color("165"), // magenta
	lipgloss.Color("118"), // lime
	lipgloss.Color("215"), // peach
	lipgloss.Color("147"), // lavender
	lipgloss.Color("129"), // purple
	lipgloss.Color("33"),  // royal blue
}

// featureColor maps a feature slug to a stable colour from
// featurePalette. Slug "" (no feature) returns the muted colour so
// unassigned issues are visually quiet. Hash collisions are accepted as
// the cost of zero configuration.
func featureColor(slug string) lipgloss.Color {
	if slug == "" {
		return mutedColor
	}
	h := fnv.New32a()
	h.Write([]byte(slug))
	return featurePalette[h.Sum32()%uint32(len(featurePalette))]
}
