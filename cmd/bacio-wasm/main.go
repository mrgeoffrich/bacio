// Command bacio-wasm is the browser build of bacio's TUI. Compiled with
// GOOS=js GOARCH=wasm, it speaks to an xterm.js terminal via a small
// syscall/js bridge: writes go to a JS callback registered as
// `bacioOnStdout`, key events arrive through `bacioWriteStdin`, and the
// program signals readiness by calling `bacioReady`.
//
// Phase 2 (this file): runs a static Bubble Tea program through the
// bridge to prove that the framework renders correctly. Phase 3 wires
// keyboard input; phase 4 swaps in the real bacio TUI.
package main

import (
	"fmt"
	"syscall/js"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func main() {
	out := newJSOut("bacioOnStdout")
	in := newJSIn() // exposed to JS as window.bacioWriteStdin

	// Tell the host we're up so it can dismiss the loading state. The
	// callback also receives the bridge so JS can push keys in later.
	if cb := js.Global().Get("bacioReady"); cb.Truthy() {
		cb.Invoke()
	}

	// Hardcode initial terminal size; phase 3 will accept resize messages
	// through the input bridge.
	p := tea.NewProgram(
		newDemoModel(),
		tea.WithInput(in),
		tea.WithOutput(out),
		// Signals aren't available in js/wasm.
		tea.WithoutSignalHandler(),
		tea.WithoutSignals(),
		// Don't try to grab the terminal — xterm.js owns it.
		tea.WithoutCatchPanics(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(out, "\r\n\x1b[31mbacio-wasm crashed: %v\x1b[0m\r\n", err)
	}
}

// demoModel is a placeholder Bubble Tea program for phase 2 — proves
// rendering works end-to-end. Real bacio TUI lands in phase 4.
type demoModel struct {
	width, height int
	frame         int
}

func newDemoModel() demoModel { return demoModel{width: 110, height: 28} }

func (m demoModel) Init() tea.Cmd { return tickCmd() }

type tickMsg time.Time

func tickCmd() tea.Cmd {
	// Pure-Go time.After works on js/wasm, so this is safe.
	return tea.Every(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m demoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case tickMsg:
		m.frame++
		return m, tickCmd()
	}
	return m, nil
}

func (m demoModel) View() string {
	gold := lipgloss.NewStyle().Foreground(lipgloss.Color("#F2DDA0")).Bold(true)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#74819A"))
	cream := lipgloss.NewStyle().Foreground(lipgloss.Color("#F9EBC4"))

	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2A344A")).
		Padding(1, 3).
		Width(min(m.width-4, 80))

	body := gold.Render("bacio · phase 2") + "\n" +
		dim.Render("Bubble Tea rendering through the WASM bridge.") + "\n\n" +
		cream.Render(fmt.Sprintf("frame %03d", m.frame)) + "  " +
		dim.Render(fmt.Sprintf("terminal: %d×%d", m.width, m.height)) + "\n\n" +
		dim.Render("press q to quit")

	return "\n" + box.Render(body) + "\n"
}
