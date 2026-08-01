package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// send drives the model the way the program would, so tests exercise the real
// Update path rather than a parallel one.
func send(m *model, msgs ...tea.Msg) *model {
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(*model)
	}
	return m
}

func sized(w, h int) *model {
	return send(newModel(), tea.WindowSizeMsg{Width: w, Height: h})
}

// Each running step gets its own line with its own status. One shared line was
// what made a real install lie: "installing LyX" and "mathtools [7/35]"
// overwrote one another while both steps were genuinely running.
func TestEachRunningStepKeepsItsOwnStatus(t *testing.T) {
	m := sized(100, 30)
	m = send(m,
		overallMsg{done: 2, total: 13},
		beginMsg{step: "LyX"},
		beginMsg{step: "Hebrew LaTeX packages"},
		detailMsg{step: "LyX", text: "downloading 41.2 MB of 57.6 MB"},
		detailMsg{step: "Hebrew LaTeX packages", text: "mathtools [7/35]"},
	)

	view := m.View()
	for _, want := range []string{"2/13", "LyX", "downloading 41.2 MB", "Hebrew LaTeX packages", "mathtools [7/35]"} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q:\n%s", want, view)
		}
	}
}

// A finished step must leave the live area, or its stale status stays on screen.
func TestFinishedStepLeavesTheLiveArea(t *testing.T) {
	m := sized(100, 30)
	m = send(m, beginMsg{step: "alpha"}, beginMsg{step: "bravo"},
		endMsg{step: "alpha", ok: true, msg: "alpha"})

	if len(m.live) != 1 || m.live[0].name != "bravo" {
		t.Fatalf("expected only bravo still running, got %v", m.live)
	}
	// It moves into the scrollback rather than vanishing.
	if !strings.Contains(strings.Join(m.history, "\n"), "alpha") {
		t.Errorf("finished step did not reach the history: %v", m.history)
	}
}

// An optional step that failed must not report success first. The old display
// printed "SUCCESS Defender exclusions" immediately above "WARNING Defender
// exclusions did not complete", which read as both at once.
func TestDroppedStepLeavesNoOutcomeLine(t *testing.T) {
	m := sized(100, 30)
	m = send(m, beginMsg{step: "Defender exclusions"},
		endMsg{step: "Defender exclusions", ok: true, msg: dropSentinel},
		noteMsg{kind: "warn", text: "Defender exclusions did not complete"})

	joined := strings.Join(m.history, "\n")
	if strings.Contains(joined, "✓") {
		t.Errorf("a dropped step still claimed success:\n%s", joined)
	}
	if !strings.Contains(joined, "did not complete") {
		t.Errorf("the warning was lost:\n%s", joined)
	}
	if strings.Contains(joined, dropSentinel) {
		t.Errorf("the sentinel leaked into the display:\n%s", joined)
	}
}

// Resizing is the whole reason for the migration: the previous display read the
// width once at startup and truncated against a number that could go stale.
func TestResizeIsHonoured(t *testing.T) {
	m := sized(120, 40)
	m = send(m, beginMsg{step: "LyX"},
		detailMsg{step: "LyX", text: strings.Repeat("long detail ", 40)})

	wide := m.View()
	m = send(m, tea.WindowSizeMsg{Width: 40, Height: 20})
	narrow := m.View()

	for _, line := range strings.Split(narrow, "\n") {
		if w := len([]rune(stripANSI(line))); w > 40 {
			t.Errorf("line is %d columns in a 40-column terminal: %q", w, line)
		}
	}
	if wide == narrow {
		t.Error("resizing did not change the rendering")
	}
}

// A question blocks the installer goroutine until a key answers it, and the
// answer must come back on the reply channel.
func TestConfirmIsAnsweredByAKeypress(t *testing.T) {
	for _, tc := range []struct {
		key  string
		def  bool
		want bool
	}{
		{"y", false, true},
		{"n", true, false},
		{"enter", true, true},
		{"enter", false, false},
	} {
		m := sized(100, 30)
		reply := make(chan bool, 1)
		m = send(m, confirmMsg{question: "Add exclusions?", detail: "why", def: tc.def, reply: reply})

		if !strings.Contains(m.View(), "Add exclusions?") {
			t.Errorf("question not shown for key %q", tc.key)
		}
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.key)})
		if tc.key == "enter" {
			m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
		}

		select {
		case got := <-reply:
			if got != tc.want {
				t.Errorf("key %q with default %v gave %v, want %v", tc.key, tc.def, got, tc.want)
			}
		default:
			t.Errorf("key %q produced no answer", tc.key)
		}
		if m.pending != nil {
			t.Errorf("question still pending after key %q", tc.key)
		}
	}
}

// Keys that mean nothing here must not be taken as an answer: a stray keypress
// silently accepting a system change would be much worse than ignoring it.
func TestUnrelatedKeysDoNotAnswerAQuestion(t *testing.T) {
	m := sized(100, 30)
	reply := make(chan bool, 1)
	m = send(m, confirmMsg{question: "Add exclusions?", def: false, reply: reply})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})

	select {
	case got := <-reply:
		t.Errorf("an unrelated key answered the question with %v", got)
	default:
	}
	if m.pending == nil {
		t.Error("the question was dismissed without being answered")
	}
}

// Scrolling up to read something must not be undone by the next step finishing.
func TestScrollingUpIsNotFought(t *testing.T) {
	m := sized(80, 12)
	for i := 0; i < 60; i++ {
		m = send(m, noteMsg{kind: "info", text: "line"})
	}
	m.vp.GotoTop()
	atTop := m.vp.YOffset

	m = send(m, noteMsg{kind: "info", text: "arrived while reading"})
	if m.vp.YOffset != atTop {
		t.Errorf("view jumped from %d to %d while the user was scrolled up", atTop, m.vp.YOffset)
	}

	// Back at the bottom, new lines should follow again.
	m.vp.GotoBottom()
	m = send(m, noteMsg{kind: "info", text: "newest"})
	if !m.vp.AtBottom() {
		t.Error("view stopped following new lines at the bottom")
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// The scrollback must not reserve height it is not using: a fixed-height
// viewport left a block of empty rows between the last line and the help text
// for most of a run.
func TestScrollbackGrowsWithContentInsteadOfPadding(t *testing.T) {
	m := sized(80, 30)
	m = send(m, noteMsg{kind: "info", text: "one"}, noteMsg{kind: "info", text: "two"})

	if m.vp.Height > len(m.history) {
		t.Errorf("viewport reserved %d rows for %d lines of history", m.vp.Height, len(m.history))
	}
	blank := 0
	for _, l := range strings.Split(m.View(), "\n") {
		if strings.TrimSpace(stripANSI(l)) == "" {
			blank++
		}
	}
	if blank > 4 {
		t.Errorf("view has %d blank lines with only two entries:\n%s", blank, m.View())
	}
}

// The banner carries the version, so a bug report says which build produced it.
func TestBannerShowsVersionAndProgress(t *testing.T) {
	m := sized(100, 30)
	m = send(m, titleMsg{version: "v0.8.0"}, overallMsg{done: 3, total: 13})
	view := stripANSI(m.View())
	for _, want := range []string{"MadLyX", "v0.8.0", "3/13"} {
		if !strings.Contains(view, want) {
			t.Errorf("banner is missing %q:\n%s", want, view)
		}
	}
}
