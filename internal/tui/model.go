package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/morks/stackit_mock/internal/store"
)

type Model struct {
	base   string // e.g. http://localhost:9001/_admin
	client *http.Client

	resources []store.Resource
	events    []store.Event
	lastSeq   uint64
	rows      []Row
	cursor    int
	filter    textinput.Model
	filtering bool
	shown     string // mock URL displayed in the header
	confirm   bool
	err       error
	w, h      int
}

func New(adminURL string) Model {
	ti := textinput.New()
	ti.Prompt = "/ "
	return Model{base: strings.TrimRight(adminURL, "/"), client: &http.Client{Timeout: 3 * time.Second}, filter: ti}
}

// WithShownURL sets the mock base URL displayed in the header.
func (m Model) WithShownURL(u string) Model { m.shown = u; return m }

// WithClient replaces the HTTP client (e.g. with an in-process transport).
func (m Model) WithClient(c *http.Client) Model { m.client = c; return m }

type tickMsg struct{}
type dataMsg struct {
	res    []store.Resource
	events []store.Event
	last   uint64
	err    error
}
type resetMsg struct{ err error }

func (m Model) Init() tea.Cmd { return tea.Batch(m.fetch(), tick()) }

func tick() tea.Cmd { return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }

func (m Model) get(path string, out any) error {
	resp, err := m.client.Get(m.base + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (m Model) fetch() tea.Cmd {
	since := m.lastSeq
	return func() tea.Msg {
		var res []store.Resource
		if err := m.get("/resources", &res); err != nil {
			return dataMsg{err: err}
		}
		var ev struct {
			Last   uint64        `json:"last"`
			Events []store.Event `json:"events"`
		}
		if err := m.get(fmt.Sprintf("/events?since=%d", since), &ev); err != nil {
			return dataMsg{err: err}
		}
		return dataMsg{res: res, events: ev.Events, last: ev.Last}
	}
}

func (m Model) reset() tea.Cmd {
	return func() tea.Msg {
		resp, err := m.client.Post(m.base+"/reset", "application/json", nil)
		if err == nil {
			resp.Body.Close()
		}
		return resetMsg{err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tickMsg:
		return m, tea.Batch(m.fetch(), tick())
	case dataMsg:
		m.err = msg.err
		if msg.err == nil {
			m.resources = msg.res
			if msg.last < m.lastSeq { // server restarted
				m.events = nil
			}
			m.events = append(m.events, msg.events...)
			if len(m.events) > 500 {
				m.events = m.events[len(m.events)-500:]
			}
			m.lastSeq = msg.last
			m.rows = BuildRows(m.resources, m.filter.Value())
			if m.cursor >= len(m.rows) {
				m.cursor = max(0, len(m.rows)-1)
			}
		}
	case resetMsg:
		m.err = msg.err
		return m, m.fetch()
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		switch k.String() {
		case "enter", "esc":
			m.filtering = false
			m.filter.Blur()
		default:
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(k)
			m.rows = BuildRows(m.resources, m.filter.Value())
			m.cursor = 0
			return m, cmd
		}
		return m, nil
	}
	if m.confirm {
		m.confirm = false
		if k.String() == "y" {
			return m, m.reset()
		}
		return m, nil
	}
	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = max(0, min(len(m.rows)-1, m.cursor+1))
	case "/":
		m.filtering = true
		m.filter.Focus()
		return m, textinput.Blink
	case "esc":
		m.filter.SetValue("")
		m.rows = BuildRows(m.resources, "")
		m.cursor = 0
	case "r":
		m.confirm = true
	}
	return m, nil
}

var (
	title  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	sel    = lipgloss.NewStyle().Reverse(true)
	dim    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	errSty = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	box    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240"))
)

func (m Model) View() string {
	if m.w == 0 {
		return "loading…"
	}
	evH := 8
	// title (1) + two bordered boxes (2+2) + help (1) = 6 extra lines
	mainH := max(5, m.h-evH-6)
	leftW := m.w/2 - 2
	rightW := m.w - leftW - 6

	// tree with scrolling window
	start := 0
	if m.cursor >= mainH {
		start = m.cursor - mainH + 1
	}
	var tree []string
	for i := start; i < len(m.rows) && len(tree) < mainH; i++ {
		row := m.rows[i]
		line := strings.Repeat("  ", row.Depth) + row.Label
		line = lipgloss.NewStyle().MaxWidth(leftW).Render(line)
		if i == m.cursor {
			line = sel.Render(line)
		}
		tree = append(tree, line)
	}
	if len(m.rows) == 0 {
		tree = append(tree, dim.Render("(no resources)"))
	}

	detail := ""
	if m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].Resource != nil {
		b, _ := json.MarshalIndent(m.rows[m.cursor].Resource, "", "  ")
		detail = string(b)
	}
	detail = clip(detail, mainH)

	var ev []string
	from := max(0, len(m.events)-evH)
	for _, e := range m.events[from:] {
		ev = append(ev, fmt.Sprintf("%s %-8s %s %s %s", e.Time.Format("15:04:05"), e.Action, e.Type, e.Name, e.Status))
	}
	evStr := lipgloss.NewStyle().MaxWidth(m.w - 4).Render(strings.Join(ev, "\n"))

	url := m.shown
	if url == "" {
		url = strings.TrimSuffix(m.base, "/_admin")
	}
	status := fmt.Sprintf("%d resources · mock URL: %s", len(m.resources), url)
	if m.err != nil {
		status = errSty.Render("disconnected (" + url + "): " + m.err.Error())
	}
	help := "↑/↓ move · / filter · esc clear · r reset · q quit"
	if m.filtering {
		help = m.filter.View()
	} else if m.confirm {
		help = errSty.Render("Reset ALL mock state? y/N")
	} else if m.filter.Value() != "" {
		help += "  [filter: " + m.filter.Value() + "]"
	}

	top := lipgloss.JoinHorizontal(lipgloss.Top,
		box.Width(leftW).Height(mainH).Render(strings.Join(tree, "\n")),
		box.Width(rightW).Height(mainH).Render(detail))
	return strings.Join([]string{
		title.Render("STACKIT Mock") + "  " + dim.Render(status),
		top,
		box.Width(m.w - 2).Height(evH).Render(evStr),
		dim.Render(help),
	}, "\n")
}

func clip(s string, n int) string {
	l := strings.Split(s, "\n")
	if len(l) > n {
		l = append(l[:n-1], "…")
	}
	return strings.Join(l, "\n")
}
