package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// keyHint is a single key/label pair shown in the footer hint bar.
type keyHint struct {
	key   string
	label string
}

// panelInnerWidth is the content width available inside a framed panel
// (terminal width minus the two vertical border cells).
func (m Model) panelInnerWidth() int {
	w := m.width
	if w <= 0 {
		w = 80
	}
	inner := w - 2
	if inner < 10 {
		inner = 10
	}
	return inner
}

// panel frames body in a rounded border with title embedded in the top edge,
// giving bada a cohesive, taskdog-like framed look. Every body line is padded
// (ANSI-aware) to the inner width so the right border stays aligned.
func (m Model) panel(title, body string) string {
	inner := m.panelInnerWidth()
	bs := m.styles.Border

	var b strings.Builder
	b.WriteString(m.panelTop(title, inner))
	b.WriteString("\n")

	left := bs.Render("│")
	right := bs.Render("│")
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		w := lipgloss.Width(line)
		if w < inner {
			line += strings.Repeat(" ", inner-w)
		} else if w > inner {
			line = truncateANSI(line, inner)
		}
		b.WriteString(left + line + right)
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(bs.Render("╰" + strings.Repeat("─", inner) + "╯"))
	return b.String()
}

// panelTop renders the rounded top edge with an embedded, accent-styled title.
func (m Model) panelTop(title string, inner int) string {
	bs := m.styles.Border
	title = strings.TrimSpace(title)
	if title == "" {
		return bs.Render("╭" + strings.Repeat("─", inner) + "╮")
	}
	label := m.styles.PanelTitle.Render(title)
	midDashes := inner - 3 - lipgloss.Width(label)
	if midDashes < 0 {
		midDashes = 0
	}
	return bs.Render("╭─ ") + label + bs.Render(" "+strings.Repeat("─", midDashes)+"╮")
}

// legendBar renders the colored status-dot legend shown beneath the task list.
func (m Model) legendBar() string {
	sep := m.styles.Muted.Render("   ")
	parts := []string{
		m.styles.Warning.Render("●") + m.styles.Muted.Render(" pending"),
		m.styles.Success.Render("●") + m.styles.Muted.Render(" done"),
		m.styles.Danger.Render("●") + m.styles.Muted.Render(" overdue"),
		m.styles.Accent.Render("●") + m.styles.Muted.Render(" topic"),
	}
	return " " + strings.Join(parts, sep)
}

// hintBar renders a row of highlighted key chips with muted labels.
func (m Model) hintBar(hints []keyHint) string {
	sep := m.styles.Muted.Render("  ")
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		cap := m.styles.KeyCap.Render(" " + h.key + " ")
		parts = append(parts, cap+" "+m.styles.KeyLabel.Render(h.label))
	}
	return " " + strings.Join(parts, sep)
}

// truncateANSI cuts a possibly styled string to a visible width, preserving
// SGR escape sequences and appending a reset so colors don't bleed.
func truncateANSI(s string, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	visible := 0
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\x1b' {
			// copy the full escape sequence verbatim
			b.WriteRune(r)
			i++
			for i < len(runes) {
				b.WriteRune(runes[i])
				if (runes[i] >= 'a' && runes[i] <= 'z') || (runes[i] >= 'A' && runes[i] <= 'Z') {
					break
				}
				i++
			}
			continue
		}
		rw := lipgloss.Width(string(r))
		if visible+rw > width {
			break
		}
		b.WriteRune(r)
		visible += rw
	}
	b.WriteString("\x1b[0m")
	return b.String()
}
