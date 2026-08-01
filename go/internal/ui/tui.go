package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// This file is the interactive display. It exists because the hand-rolled
// version could not survive the two things that actually happen during an
// install: the terminal being resized, and the user wanting to look back at
// something that has scrolled away. Both come free here.
//
// The shape is the standard Elm loop. What matters for this program is the
// direction of travel: the installer runs on its own goroutine and only ever
// sends messages, and the model only ever renders. Nothing in steps/ knows this
// file exists - it talks to the Reporter interface exactly as before.

// Messages the installer sends to the display.
type (
	beginMsg struct{ step string }
	endMsg   struct {
		step, msg string
		ok        bool
	}
	detailMsg  struct{ step, text string }
	overallMsg struct{ done, total int }
	noteMsg    struct {
		kind string // "info", "warn", "skip", "section"
		text string
	}
	summaryMsg struct{ rows [][]string }
	// confirmMsg carries its own reply channel, which is what lets Confirm
	// block the installer goroutine while the user decides without the display
	// freezing.
	confirmMsg struct {
		question, detail string
		def              bool
		reply            chan bool
	}
)

var (
	styleHeader   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	styleStep     = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	styleDetail   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styleOK       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styleWarn     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleInfo     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styleQuestion = lipgloss.NewStyle().Bold(true)
	styleKeys     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

// running is one step currently in flight.
type running struct {
	name    string
	detail  string
	started time.Time
}

type model struct {
	spin   spinner.Model
	bar    progress.Model
	vp     viewport.Model
	ready  bool
	width  int
	height int

	history []string // durable lines, shown in the scrollback
	live    []*running
	done    int
	total   int

	pending *confirmMsg
	summary [][]string
	quit    bool
}

func newModel() *model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	b := progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage())
	b.Width = 30
	return &model{spin: s, bar: b, width: 100, height: 24}
}

func (m *model) Init() tea.Cmd { return m.spin.Tick }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// The reason this migration was worth doing: the previous display read
		// the terminal width once at startup and truncated against a number
		// that could already be wrong.
		m.width, m.height = msg.Width, msg.Height
		m.bar.Width = clamp(m.width/3, 10, 40)
		m.layout()
		return m, nil

	case tea.KeyMsg:
		if m.pending != nil {
			return m, m.answer(msg.String())
		}
		switch msg.String() {
		case "ctrl+c":
			m.quit = true
			return m, tea.Quit
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg) // scrollback: arrows, pgup/pgdn, home/end
		return m, cmd

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case beginMsg:
		m.live = append(m.live, &running{name: msg.step, started: time.Now()})
		return m, nil

	case detailMsg:
		if r := m.find(msg.step); r != nil {
			r.detail = msg.text
		}
		return m, nil

	case endMsg:
		elapsed := ""
		if r := m.find(msg.step); r != nil {
			elapsed = " " + humanDuration(time.Since(r.started))
		}
		m.drop(msg.step)
		// An optional step that failed leaves no outcome line; the warning that
		// follows is the outcome.
		if msg.msg == dropSentinel {
			return m, nil
		}
		line := msg.msg
		if line == "" {
			line = msg.step
		}
		if msg.ok {
			m.push(styleOK.Render("  ✓ ") + line + styleDetail.Render(elapsed))
		} else {
			m.push(styleErr.Render("  ✗ ") + line)
		}
		return m, nil

	case overallMsg:
		m.done, m.total = msg.done, msg.total
		return m, nil

	case noteMsg:
		switch msg.kind {
		case "warn":
			m.push(styleWarn.Render("  ! ") + msg.text)
		case "skip":
			m.push(styleInfo.Render("  · " + msg.text))
		case "section":
			m.push("")
			m.push(styleHeader.Render(msg.text))
		default:
			m.push(styleInfo.Render("    " + msg.text))
		}
		return m, nil

	case confirmMsg:
		m.pending = &msg
		return m, nil

	case summaryMsg:
		m.summary = msg.rows
		return m, nil

	case tea.QuitMsg:
		m.quit = true
		return m, tea.Quit
	}
	return m, nil
}

// answer resolves a pending question from a keypress.
func (m *model) answer(key string) tea.Cmd {
	var got bool
	switch strings.ToLower(key) {
	case "y":
		got = true
	case "n":
		got = false
	case "enter":
		got = m.pending.def
	case "ctrl+c":
		got = false
	default:
		return nil // ignore anything else rather than guessing
	}
	shown := "no"
	if got {
		shown = "yes"
	}
	m.push(styleQuestion.Render("  ? "+m.pending.question) + styleDetail.Render("  "+shown))
	m.pending.reply <- got
	m.pending = nil
	return nil
}

func (m *model) find(step string) *running {
	for _, r := range m.live {
		if r.name == step {
			return r
		}
	}
	return nil
}

func (m *model) drop(step string) {
	for i, r := range m.live {
		if r.name == step {
			m.live = append(m.live[:i], m.live[i+1:]...)
			return
		}
	}
}

// push adds a durable line and keeps the view pinned to the newest unless the
// user has scrolled up to read something.
func (m *model) push(line string) {
	atBottom := !m.ready || m.vp.AtBottom()
	m.history = append(m.history, line)
	if m.ready {
		m.vp.SetContent(strings.Join(m.history, "\n"))
		if atBottom {
			m.vp.GotoBottom()
		}
	}
}

func (m *model) layout() {
	h := m.height - m.chromeHeight()
	if h < 3 {
		h = 3
	}
	if !m.ready {
		m.vp = viewport.New(m.width, h)
		m.ready = true
	} else {
		m.vp.Width, m.vp.Height = m.width, h
	}
	m.vp.SetContent(strings.Join(m.history, "\n"))
	m.vp.GotoBottom()
}

// chromeHeight is everything that is not scrollback: header, live steps, help.
func (m *model) chromeHeight() int {
	n := 2 + len(m.live) + 2
	if m.pending != nil {
		n += 3 + strings.Count(m.pending.detail, "\n")
	}
	return n
}

func (m *model) View() string {
	if m.quit {
		return ""
	}
	var b strings.Builder

	b.WriteString(styleHeader.Render(fmt.Sprintf("MadLyX  %d/%d steps", m.done, m.total)))
	b.WriteString("\n\n")

	if m.ready {
		b.WriteString(m.vp.View())
		b.WriteString("\n")
	}

	for _, r := range m.live {
		line := m.spin.View() + " " + styleStep.Render(r.name)
		if r.detail != "" {
			line += styleDetail.Render("  " + r.detail)
		}
		line += styleDetail.Render("  " + humanDuration(time.Since(r.started)))
		b.WriteString(truncANSI(line, m.width) + "\n")
	}

	if m.pending != nil {
		b.WriteString("\n" + styleQuestion.Render(" ? "+m.pending.question) + "\n")
		for _, l := range strings.Split(strings.TrimSpace(m.pending.detail), "\n") {
			b.WriteString(styleDetail.Render("   "+l) + "\n")
		}
		hint := "[y/N]"
		if m.pending.def {
			hint = "[Y/n]"
		}
		b.WriteString(styleKeys.Render("   "+hint+"  enter accepts the default") + "\n")
	} else {
		b.WriteString(styleKeys.Render("  ↑/↓ scroll · ctrl+c cancel") + "\n")
	}
	return b.String()
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// truncANSI shortens a rendered line to n visible columns, ignoring escapes.
func truncANSI(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	// Rebuilding by visible width is fiddly; lipgloss does it correctly.
	return lipgloss.NewStyle().MaxWidth(n).Render(s)
}
