// Logview is a [bubbletea.Model] optimized for displaying logs.
package logview

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"

	"cmp"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/truncate"
	"github.com/muesli/reflow/wrap"
)

type Styles struct {
	Log       lipgloss.Style
	Statusbar lipgloss.Style
}

var defaultStyles = &Styles{
	Log:       lipgloss.NewStyle(),
	Statusbar: lipgloss.NewStyle(),
}

func (m *Model) View() string {
	return m.Render(defaultStyles, m.windowWidth, m.windowHeight)
}

func (m *Model) Render(styles *Styles, width, height int) string {
	// don't crash if window has zero area
	if width <= 0 || height <= 0 {
		return ""
	}

	// skip statusbar if window is too short
	if height < 2 || !m.shouldShowStatusbar {
		content := m.RenderLog(width, height)
		logStyle := styles.Log.Copy().
			Width(width).Height(height).
			MaxWidth(width).MaxHeight(height)
		return logStyle.Render(content)
	}

	// render logview and statusbar
	content := m.RenderLog(width, height-1)
	logStyle := styles.Log.Copy().
		Width(width).Height(height - 1).
		MaxWidth(width).MaxHeight(height - 1)
	logview := logStyle.Render(content)
	statusbar := styles.Statusbar.Copy().
		Width(width).Height(1).
		MaxWidth(width).MaxHeight(1).
		Render(m.viewStatusbar())
	return logview + "\n" + statusbar
}

func (m *Model) viewStatusbar() string {
	result := m.RenderLineStatus()
	if result != "" {
		result += "\t"
	}
	result += m.RenderSearchStatus()
	return result
}

func (m *Model) RenderLineStatus() string {
	lineView := m.lines
	if m.queryRe != nil {
		lineView = m.filtered
	}
	linecount := len(lineView)
	if m.buffer != "" {
		linecount += 1
	}

	if m.scrollPosition < 0 {
		return ""
	}
	return fmt.Sprintf("%d of %d", m.scrollPosition+1, linecount)
}

func (m *Model) RenderSearchStatus() string {
	var out string
	if m.Query() != "" || m.focus == FocusSearchBar {
		out += m.input.View()
	}
	return out
}

func (m *Model) RenderLog(width, height int) string {
	// If we're tailing, start assembling output from the -end- of the log,
	// returning it when we have enough

	lineView := m.lines
	if m.queryRe != nil {
		lineView = m.filtered
	}
	if m.scrollPosition < 0 {
		var (
			linecount    = len(lineView)
			pointer      = linecount - 1
			output       = ""
			outputHeight = 0
			targetHeight = height
		)

		// handle the buffer, if present
		if m.buffer != "" {
			wrapped, wrappedHeight := m.wrapLine(m.buffer, targetHeight, width)
			output = "\n" + wrapped
			outputHeight = wrappedHeight
		}

		for ; outputHeight < targetHeight && pointer >= 0; pointer-- {
			l := lineView[pointer]
			wrapped, wrappedHeight := m.wrapLine(l, targetHeight-outputHeight, width)
			output = "\n" + wrapped + output
			outputHeight += wrappedHeight
		}
		m.firstDisplayedLine = pointer

		output = strings.TrimPrefix(output, "\n")

		if outputHeight < targetHeight {
			pad := strings.Repeat(" ", width) + "\n"
			for outputHeight < targetHeight {
				output = pad + output
				outputHeight += 1
			}
		}

		return output
	}

	// If we're not tailing, start from m.scrollPosition and keep adding
	// wrapped output until we reach m.logHeight
	var (
		linecount    = len(lineView)
		pointer      = m.scrollPosition
		output       = ""
		outputHeight = 0
		targetHeight = m.logHeight(height)
	)

	m.firstDisplayedLine = m.scrollPosition

	// handle the lines
	for ; outputHeight < targetHeight && pointer < linecount; pointer++ {
		l := lineView[pointer]
		wrapped, wrappedHeight := m.wrapLine(l, targetHeight-outputHeight, width)
		output = output + wrapped + "\n"
		outputHeight += wrappedHeight
	}

	// handle the buffer
	if outputHeight < targetHeight && m.buffer != "" {
		l := m.buffer
		wrapped, wrappedHeight := m.wrapLine(l, targetHeight-outputHeight, width)
		output = output + wrapped + "\n"
		outputHeight += wrappedHeight
	}

	return strings.TrimSuffix(output, "\n")
}

func (m *Model) wrapLine(line string, maxLines, width int) (string, int) {
	if m.shouldHardwrap {
		wrapped := truncate.String(line, uint(width))
		return wrapped, 1
	} else {
		wrapped := wrap.String(line, width)
		wrappedHeight := strings.Count(wrapped, "\n") + 1
		if wrappedHeight > maxLines {
			wrappedHeight = maxLines
			if m.scrollPosition < 0 {
				wrapped = lastNLines(wrapped, wrappedHeight)
			} else {
				wrapped = firstNLines(wrapped, wrappedHeight)
			}
		}
		return wrapped, wrappedHeight
	}
}

func firstNLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	return strings.Join(lines[:min(n, len(lines))], "\n")
}

func lastNLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

var highlight = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#dddd44"))

func modulo(i, n int) int {
	if n == 0 {
		return 0
	}
	return ((i % n) + n) % n
}

func clamp[T cmp.Ordered](lower, upper, val T) T {
	return max(lower, min(upper, val))
}

func (m *Model) search() []string {
	if m.queryRe == nil {
		return nil
	}

	var results []string
	for i := range m.lines {
		if thing := m.searchLine(i); thing != nil {
			results = append(results, *thing)
		}
	}
	return results
}

func (m *Model) searchLine(lineno int) *string {
	if m.queryRe == nil {
		return nil
	}

	var line string
	if lineno < 0 {
		return nil
	} else {
		line = m.lines[lineno]
	}
	var result *string
	start := 0
	for _, m := range m.queryRe.FindAllStringIndex(line, -1) {
		if m[0] > start {
			stuff := line[start:m[0]]
			if result != nil {
				stuff = *result + stuff
			}
			result = &stuff
		}
		start = m[1]
		stuff := highlight.Render(line[m[0]:m[1]])
		if result != nil {
			stuff = *result + stuff
		}
		result = &stuff
		// TODO: fix me
		// results = append(results, searchResult{
		// 	line:   lineno,
		// 	char:   m[0],
		// 	length: m[1] - m[0],
		// })
	}
	if result != nil {
		stuff := *result + line[start:]
		result = &stuff
	}
	return result
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		cmd := m.handleKey(msg)
		return m, cmd
	case tea.MouseMsg:
		m.handleMouse(msg)
	default:
		newInput, cmd := m.input.Update(msg)
		m.input = &newInput
		return m, cmd
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	if m.focus == FocusHelp {
		switch msg.String() {
		case "esc", "ctrl+c", "q", "h", "?":
			m.SetFocus(FocusLogPane)
		}
		return nil
	}
	if m.focus == FocusSearchBar {
		switch msg.String() {
		case "esc", "ctrl+c":
			m.input.SetValue(m.prevQuery)
			m.prevQuery = ""
			m.SetFocus(FocusLogPane)
		case "enter":
			m.SetFocus(FocusLogPane)
		case "backspace":
			if m.Query() == "" {
				m.SetFocus(FocusLogPane)
				return nil
			}
			fallthrough
		default:
			queryBefore := m.input.Value()
			newSearch, cmd := m.input.Update(msg)
			m.input = &newSearch
			if newSearch.Value() != queryBefore {
				m.handleSearch()
			}
			return cmd
		}
		return nil
	}

	switch msg.String() {
	case "ctrl+c", "esc":
		return tea.Quit
	case "w":
		m.SetWrapMode(!m.shouldHardwrap)
	case "/":
		m.prevQuery = m.Query()
		m.SetQuery("")
		m.SetFocus(FocusSearchBar)

	case "up", "k":
		m.ScrollBy(-1)
	case "down", "j":
		m.ScrollBy(1)

	case "pgup":
		pageDistance := max(0, m.windowHeight-4)
		m.ScrollBy(-pageDistance)
	case "pgdown":
		pageDistance := max(0, m.windowHeight-4)
		m.ScrollBy(pageDistance)

	case "ctrl+u":
		m.ScrollBy(-m.windowHeight / 2)
	case "ctrl+d":
		m.ScrollBy(m.windowHeight / 2)

	case "home":
		m.ScrollTo(0)
	case "end":
		m.ScrollTo(-1)

	case "g":
		if m.heldKey == "g" {
			m.ScrollTo(0)
			m.heldKey = ""
		} else {
			m.heldKey = "g"
		}
	case "G":
		m.ScrollTo(-1)
	}
	return nil
}

func (m *Model) handleMouse(msg tea.MouseMsg) {
	switch msg.Button {
	case tea.MouseButtonWheelDown:
		m.ScrollBy(1)
	case tea.MouseButtonWheelUp:
		m.ScrollBy(-1)
	}
}

func (m *Model) handleWrite(content string) {
	scanner := bufio.NewScanner(strings.NewReader(content))

	// In order to deal with an existing buffer, we'll manually handle the
	// first line before looping over the rest of the scan.

	// If the write is just the empty string, there's nothing to do.
	if !scanner.Scan() {
		return
	}

	// If the first thing we scan is a newline, flush the buffer.
	// Otherwise, add it to the buffer and then flush.
	text := scanner.Text()
	m.lines, m.buffer = append(m.lines, m.buffer+text), ""
	if result := m.searchLine(len(m.lines) - 1); result != nil {
		m.filtered = append(m.filtered, *result)
	}

	// Now handle the rest of the lines.
	for scanner.Scan() {
		text := scanner.Text()
		m.lines = append(m.lines, text)
		if result := m.searchLine(len(m.lines) - 1); result != nil && strings.HasSuffix(text, "\n") {
			m.filtered = append(m.filtered, *result)
		}
	}
	if err := scanner.Err(); err != nil {
		panic(err)
	}

	// If the write didn't end with a newline, we overshot: the last line
	// we scanned should actually be the new buffer.
	if len(m.lines) > 0 && !strings.HasSuffix(content, "\n") {
		m.buffer = m.lines[len(m.lines)-1]
		m.lines = m.lines[:len(m.lines)-1]
	}
}

func (m *Model) handleSearch() {
	query := m.input.Value()

	if query == "" {
		m.queryRe = nil
		return
	}

	if queryRe, err := regexp.Compile(query); err == nil {
		m.queryRe = queryRe
	}
	m.filtered = m.search()
}

func New(mods ...func(*Model)) *Model {
	inp := textinput.New()
	inp.Prompt = "/"

	m := &Model{
		scrollPosition:      -1,
		shouldShowStatusbar: true,
		input:               &inp,
	}
	for _, mod := range mods {
		mod(m)
	}
	return m
}

func WithoutStatusbar(m *Model)    { m.shouldShowStatusbar = false }
func WithStartAtHead(m *Model)     { m.scrollPosition = 0 }
func WithSoftWrap(m *Model) *Model { m.shouldHardwrap = false; return m }

// [Model] implements [tea.Model]
var _ tea.Model = &Model{}

type Model struct {
	windowWidth  int
	windowHeight int

	shouldHardwrap      bool
	shouldShowStatusbar bool

	focus FocusArea

	input     *textinput.Model
	queryRe   *regexp.Regexp
	prevQuery string

	// state for two-key inputs like `gg`
	heldKey string

	// ScrollPosition tracks the position of the viewport relative to the
	// log's content.
	//  - If it's negative, we are tailing the log.
	//  - If it's positive, the scrollPositionth line is pinned to the top
	//    of the viewport.
	// For example, if scrollPosition is 0, the 0th line is displayed at
	// the top of the viewport.
	scrollPosition int

	firstDisplayedLine int

	// lines contains all complete lines (that is, a "\n" was written to
	// end the line).
	lines    []string
	filtered []string

	// If the most recent character written was not a "\n", buffer contains
	// everything that was written since the last "\n".
	buffer string
}

func (m *Model) Init() tea.Cmd {
	return nil
}

func (m *Model) String() string {
	return strings.Join(m.content(), "\n")
}

func (m *Model) Write(content string) { m.handleWrite(content) }

func (m *Model) SetDimensions(width, height int) { m.windowWidth, m.windowHeight = width, height }

func (m *Model) Query() string {
	return m.input.Value()
}

func (m *Model) Focus() FocusArea {
	return m.focus
}

func (m *Model) SetFocus(focus FocusArea) {
	m.focus = focus
	switch focus {
	case FocusSearchBar:
		m.input.Focus()
		m.handleSearch()
	default:
		m.input.Blur()
	}
}

func (m *Model) SetQuery(query string) {
	m.input.SetValue(query)
	m.handleSearch()
}

func (m *Model) ScrollBy(lines int) {
	lineView := m.lines
	if m.queryRe != nil {
		lineView = m.filtered
	}
	// if tailing, first set scroll position to the bottom before adjusting it.
	if m.scrollPosition < 0 {
		m.scrollPosition = max(0, m.firstDisplayedLine)
	}

	// update scroll position
	m.scrollPosition = clamp(0, len(lineView)-1, m.scrollPosition+lines)
}

func (m *Model) ScrollTo(line int) {
	lineView := m.lines
	if m.queryRe != nil {
		lineView = m.filtered
	}
	if line < 0 {
		m.scrollPosition = -1
	} else {
		m.scrollPosition = clamp(0, len(lineView)-1, line)
	}
}

func (m *Model) ShowStatusbar(show bool) { m.shouldShowStatusbar = show }

func (m *Model) SetWrapMode(hardwrap bool) { m.shouldHardwrap = hardwrap }
func (m *Model) ToggleWrapMode()           { m.shouldHardwrap = !m.shouldHardwrap }

func (m *Model) logHeight(height int) int {
	if !m.shouldShowStatusbar {
		return height
	}
	return max(height-1, 0)
}

func (m *Model) content() []string {
	lines := m.lines
	if m.buffer != "" {
		lines = append(m.lines, m.buffer)
	}
	return lines
}

type FocusArea int

const (
	focusNone FocusArea = iota
	FocusSearchBar
	FocusLogPane
	FocusHelp
)
