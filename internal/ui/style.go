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

// panelInnerWidth is the content width available inside a framed panel.
//
// Leave two terminal cells unused on the right. Writing near the bottom/right
// edge can put some terminals into pending-wrap (especially with styled Gantt
// cells), which shows up as a duplicated status bar on the next repaint. The
// status bar uses the same spare-cell rule.
func (m Model) panelInnerWidth() int {
	w := m.width
	if w <= 0 {
		w = 80
	}
	inner := w - 4 // two borders plus two spare anti-wrap cells
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

// hintBar renders a row of highlighted key chips with muted labels.
func (m Model) hintBar(hints []keyHint) string {
	sep := m.styles.Muted.Render("  ")
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		cap := m.styles.KeyCap.Render(" " + h.key + " ")
		parts = append(parts, cap+" "+m.styles.KeyLabel.Render(h.label))
	}
	out := " " + strings.Join(parts, sep)
	// Clip two cells short of the terminal width so a long hint row can't wrap onto
	// a second line or trigger pending-wrap at the right edge.
	if m.width > 0 {
		maxWidth := m.width - 2
		if maxWidth < 1 {
			maxWidth = 1
		}
		if lipgloss.Width(out) > maxWidth {
			out = truncateANSI(out, maxWidth)
		}
	}
	return out
}

// stripeLine re-applies style's background across an already-rendered line so a
// zebra tint survives the inner ANSI resets emitted by per-cell styling
// (lipgloss drops the background after each reset, leaving gaps otherwise). When
// the style carries no background (e.g. the color is empty or stripped in a
// non-TTY) it returns the line unchanged.
func stripeLine(style lipgloss.Style, line string) string {
	open := bgOpenSeq(style)
	if open == "" {
		return line
	}
	return open + strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+open) + "\x1b[0m"
}

// bgOpenSeq extracts the leading SGR escape a style emits before its content, by
// probing with a sentinel rune that never appears inside an escape sequence.
func bgOpenSeq(style lipgloss.Style) string {
	probe := style.Render("M")
	if idx := strings.IndexByte(probe, 'M'); idx > 0 {
		return probe[:idx]
	}
	return ""
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
