// Command bacio-wasm is the browser build of bacio's TUI. Compiled with
// GOOS=js GOARCH=wasm, it speaks to an xterm.js terminal via a small
// syscall/js bridge:
//
//   window.bacioOnStdout(string)  — Go writes; called from Go.
//   window.bacioWriteStdin(string) — JS feeds Go keypresses; xterm.js
//                                    handles escape-sequence encoding.
//   window.bacioResize(cols, rows) — JS pushes a tea.WindowSizeMsg so
//                                    the layout reflows.
//   window.bacioReady()            — Go signals "I'm up" to JS.
//
// Phase 3 (this file): real keyboard input wired through. A demo model
// with four tabs (Issues / Features / Documents / History), j/k/h/l
// navigation in the kanban, and 1–4 tab switching.
//
// The real bacio TUI lands in phase 4.
package main

import (
	"fmt"
	"strings"
	"syscall/js"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func main() {
	out := newJSOut("bacioOnStdout")
	in := newJSIn() // exposed to JS as window.bacioWriteStdin

	p := tea.NewProgram(
		newDemoModel(),
		tea.WithInput(in),
		tea.WithOutput(out),
		tea.WithoutSignalHandler(),
		tea.WithoutSignals(),
		tea.WithoutCatchPanics(),
	)

	// Expose window.bacioResize so the JS host can push a WindowSizeMsg
	// when xterm.js resizes the terminal. Bubble Tea's standard renderer
	// otherwise wouldn't know about size changes on js/wasm.
	js.Global().Set("bacioResize", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 2 {
			return nil
		}
		p.Send(tea.WindowSizeMsg{
			Width:  args[0].Int(),
			Height: args[1].Int(),
		})
		return nil
	}))

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(out, "\r\n\x1b[31mbacio-wasm crashed: %v\x1b[0m\r\n", err)
	}
}

type readyMsg struct{}

// signalReady notifies the JS host that we're up. It runs as a tea.Cmd
// from Init so by the time JS pushes its first bacioResize back at us
// the program's event loop is already draining p.msgs — otherwise
// Send would deadlock on the unbuffered channel.
func signalReady() tea.Msg {
	if cb := js.Global().Get("bacioReady"); cb.Truthy() {
		cb.Invoke()
	}
	return readyMsg{}
}

// ---------------------------------------------------------------------------
// Demo model (placeholder until the real bacio TUI lands in phase 4)
// ---------------------------------------------------------------------------

type demoTab int

const (
	tabIssues demoTab = iota
	tabFeatures
	tabDocuments
	tabHistory
)

func (t demoTab) name() string {
	switch t {
	case tabIssues:
		return "Issues"
	case tabFeatures:
		return "Features"
	case tabDocuments:
		return "Documents"
	case tabHistory:
		return "History"
	}
	return ""
}

type demoColumn struct {
	title  string
	colour lipgloss.Color
	items  []string
}

type demoModel struct {
	width, height int
	tab           demoTab
	cols          []demoColumn
	cursorCol     int // 0..len(cols)-1
	cursorItem    []int
}

func newDemoModel() demoModel {
	cols := []demoColumn{
		{"Backlog", "#F0C8D2", []string{
			"BACIO-01 · Banana", "BACIO-02 · Pesca e Mango",
			"BACIO-03 · Visciole", "BACIO-04 · Limone",
			"BACIO-05 · Yogurt", "BACIO-06 · Caramello Salato",
		}},
		{"Todo", "#F2DDA0", []string{
			"BACIO-07 · Vaniglia", "BACIO-08 · Fior di Latte",
			"BACIO-09 · Caffè", "BACIO-10 · Stracciatella",
			"BACIO-11 · Crème Caramel", "BACIO-12 · Amaretto",
		}},
		{"In Progress", "#C2DFB8", []string{
			"BACIO-13 · Cioccolato", "BACIO-14 · Fondente",
			"BACIO-15 · Pistacchio", "BACIO-19 · Bacio",
			"BACIO-17 · Nutella", "BACIO-18 · Valentino",
		}},
		{"Done", "#D9C4F1", []string{
			"BACIO-16 · Nocciola", "BACIO-20 · Bacio di Noce",
			"BACIO-21 · Desire", "BACIO-22 · Tiramisu",
			"BACIO-23 · Cannolo", "BACIO-24 · Kinderino",
		}},
	}
	return demoModel{
		width:      110,
		height:     28,
		tab:        tabIssues,
		cols:       cols,
		cursorCol:  0,
		cursorItem: make([]int, len(cols)),
	}
}

func (m demoModel) Init() tea.Cmd { return signalReady }

func (m demoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "1":
			m.tab = tabIssues
		case "2":
			m.tab = tabFeatures
		case "3":
			m.tab = tabDocuments
		case "4":
			m.tab = tabHistory
		case "h", "left":
			if m.tab == tabIssues && m.cursorCol > 0 {
				m.cursorCol--
			}
		case "l", "right":
			if m.tab == tabIssues && m.cursorCol < len(m.cols)-1 {
				m.cursorCol++
			}
		case "j", "down":
			if m.tab == tabIssues {
				col := &m.cols[m.cursorCol]
				if m.cursorItem[m.cursorCol] < len(col.items)-1 {
					m.cursorItem[m.cursorCol]++
				}
			}
		case "k", "up":
			if m.tab == tabIssues {
				if m.cursorItem[m.cursorCol] > 0 {
					m.cursorItem[m.cursorCol]--
				}
			}
		}
	}
	return m, nil
}

func (m demoModel) View() string {
	var b strings.Builder
	b.WriteString(m.renderTabStrip())
	b.WriteString("\n\n")
	switch m.tab {
	case tabIssues:
		b.WriteString(m.renderBoard())
	default:
		b.WriteString(m.renderPlaceholder())
	}
	b.WriteString("\n")
	b.WriteString(m.renderFooter())
	return b.String()
}

func (m demoModel) renderTabStrip() string {
	tabs := []demoTab{tabIssues, tabFeatures, tabDocuments, tabHistory}
	parts := make([]string, 0, len(tabs))
	active := lipgloss.NewStyle().Foreground(lipgloss.Color("#FBF6EC")).Bold(true).
		Underline(true).UnderlineSpaces(false)
	inactive := lipgloss.NewStyle().Foreground(lipgloss.Color("#586581"))
	for i, t := range tabs {
		label := fmt.Sprintf("%d %s", i+1, t.name())
		if t == m.tab {
			parts = append(parts, active.Render(label))
		} else {
			parts = append(parts, inactive.Render(label))
		}
	}
	return "  " + strings.Join(parts, "    ")
}

func (m demoModel) renderBoard() string {
	const gap = 2
	innerW := m.width - 4 // page padding
	colW := (innerW - gap*(len(m.cols)-1)) / len(m.cols)
	if colW < 16 {
		colW = 16
	}

	rendered := make([]string, len(m.cols))
	for i, col := range m.cols {
		rendered[i] = m.renderColumn(col, i, colW)
	}
	return "  " + lipgloss.JoinHorizontal(lipgloss.Top, joinWithGap(rendered, gap)...)
}

func (m demoModel) renderColumn(col demoColumn, idx, width int) string {
	header := lipgloss.NewStyle().
		Foreground(col.colour).Bold(true).
		Width(width).
		Render(strings.ToUpper(col.title) + fmt.Sprintf(" · %d", len(col.items)))

	var items []string
	for i, it := range col.items {
		row := lipgloss.NewStyle().Foreground(lipgloss.Color("#DDE4EE"))
		isCursor := idx == m.cursorCol && i == m.cursorItem[idx]
		if isCursor {
			row = row.Foreground(lipgloss.Color("#1B2336")).
				Background(col.colour).Bold(true)
		}
		// Truncate to width-2 to leave room for the padding cell.
		text := it
		if len(text) > width-2 {
			text = text[:width-2]
		}
		items = append(items, row.Width(width).Render(" "+text))
	}
	content := header + "\n" + lipgloss.NewStyle().
		Foreground(lipgloss.Color("#2A344A")).
		Render(strings.Repeat("─", width)) + "\n" +
		strings.Join(items, "\n")
	return content
}

func (m demoModel) renderPlaceholder() string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#586581")).Italic(true)
	return "  " + dim.Render(m.tab.name()+" tab — real content lands in phase 4.")
}

func (m demoModel) renderFooter() string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#586581"))
	hot := lipgloss.NewStyle().Foreground(lipgloss.Color("#F2DDA0"))
	hints := []string{
		hot.Render("h/l") + dim.Render(" cols"),
		hot.Render("j/k") + dim.Render(" cards"),
		hot.Render("1–4") + dim.Render(" tabs"),
		hot.Render("q") + dim.Render(" quit"),
	}
	return "  " + strings.Join(hints, "  ·  ")
}

func joinWithGap(parts []string, gap int) []string {
	if len(parts) <= 1 {
		return parts
	}
	out := make([]string, 0, len(parts)*2-1)
	spacer := strings.Repeat(" ", gap)
	for i, p := range parts {
		if i > 0 {
			out = append(out, spacer)
		}
		out = append(out, p)
	}
	return out
}
