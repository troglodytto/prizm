// Package style is prizm's single source of visual language. Every
// user-facing line — plain text from the CLI, rendered screens from the TUI —
// uses these glyphs, colours and widths, so the tool looks like one tool.
//
// Colour is drawn from the terminal's own 16-colour palette rather than fixed
// hex values. A user has already chosen colours they can read; a tool that
// hardcodes its own fights that choice, and looks wrong in every scheme but
// the one it was designed against. It also means prizm degrades correctly on
// a 16-colour terminal, and Lip Gloss drops colour entirely when the output
// is not a terminal or NO_COLOR is set.
package style

import (
	"hash/fnv"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The palette, as ANSI indices. Colour is spent on one thing — outcome — and
// everything else is carried by weight and dimming, so nothing competes with
// a red for attention.
var (
	Red    = lipgloss.Color("1")
	Green  = lipgloss.Color("2")
	Yellow = lipgloss.Color("3")
	Blue   = lipgloss.Color("4")
	Purple = lipgloss.Color("5")
	Cyan   = lipgloss.Color("6")
	Base   = lipgloss.Color("0") // for text on an inverted badge
)

// MinWidth is the narrowest a name column gets; wider names push it out.
//
// It is 16 because separate invocations cannot align with each other — five
// `add-repo` calls are five processes — and a session's worth of one-off
// lines should still read as a column. Most repo names fit; longer ones
// widen their own line rather than being cut.
const MinWidth = 16

var (
	// A heading is bold and uncoloured: it is structure, not status.
	headingStyle = lipgloss.NewStyle().Bold(true)
	detailStyle  = lipgloss.NewStyle().Faint(true)
	hintStyle    = lipgloss.NewStyle().Faint(true).Italic(true)
	alertStyle   = lipgloss.NewStyle().Bold(true).Foreground(Red)

	sectionStyle = lipgloss.NewStyle().Faint(true)
	commandStyle = lipgloss.NewStyle().Bold(true)
	countStyle   = lipgloss.NewStyle().Faint(true)

	failDetailStyle = lipgloss.NewStyle().Foreground(Red)

	okStyle     = lipgloss.NewStyle().Foreground(Green)
	failStyle   = lipgloss.NewStyle().Bold(true).Foreground(Red)
	warnStyle   = lipgloss.NewStyle().Foreground(Yellow)
	addStyle    = lipgloss.NewStyle().Foreground(Green)
	removeStyle = lipgloss.NewStyle().Foreground(Red)
	changeStyle = lipgloss.NewStyle().Foreground(Yellow)
	plainStyle  = lipgloss.NewStyle()
)

// Mark is the leading glyph on a status line.
type Mark int

const (
	// OK is a completed action.
	OK Mark = iota
	// Fail is an action that did not happen.
	Fail
	// Warn is something that happened but deserves attention.
	Warn
	// Add is a key that appeared.
	Add
	// Remove is a key that went away.
	Remove
	// Change is a key whose value differs.
	Change
	// Same is an unchanged item.
	Same
	// Ask is something prizm will not decide on its own.
	Ask
)

// Glyph renders the mark.
func (m Mark) Glyph() string {
	switch m {
	case OK:
		return okStyle.Render("✓")
	case Fail:
		return failStyle.Render("✗")
	case Warn:
		return warnStyle.Render("⚠")
	case Add:
		return addStyle.Render("+")
	case Remove:
		return removeStyle.Render("-")
	case Change:
		return changeStyle.Render("~")
	case Same:
		return plainStyle.Render("=")
	case Ask:
		return warnStyle.Render("?")
	}
	return " "
}

// Column is a measured name-column width. Take one with WidthOf so a long
// name like "search-svc" widens the column instead of pushing every
// other row's detail out of alignment.
type Column int

// WidthOf measures the widest name, never going below MinWidth.
func WidthOf(names []string) Column {
	w := MinWidth
	for _, n := range names {
		if n := lipgloss.Width(n); n > w {
			w = n
		}
	}
	return Column(w)
}

// Row is the standard status line: a mark, a name padded to the column, and
// a detail.
//
// The detail is dimmed for outcomes you skim past and left legible for the
// ones you have to read. Dimming a failure's message hides the single most
// important line on screen.
func (c Column) Row(m Mark, name, detail string) string {
	line := m.Glyph() + " " + pad(name, int(c))
	if detail == "" {
		return strings.TrimRight(line, " ")
	}
	return line + " " + m.detailStyle().Render(detail)
}

// detailStyle picks how loudly a mark's detail is rendered.
func (m Mark) detailStyle() lipgloss.Style {
	switch m {
	case Fail:
		return failDetailStyle
	case Warn, Ask:
		return plainStyle
	default:
		return detailStyle
	}
}

// Field is a Row without a mark, for listings that are not outcomes.
func (c Column) Field(name, detail string) string {
	return "    " + pad(name, int(c)) + " " + detailStyle.Render(detail)
}

// Row renders at the default width, for one-off lines with nothing to align to.
func Row(m Mark, name, detail string) string {
	return Column(MinWidth).Row(m, name, detail)
}

func pad(s string, n int) string {
	if fill := n - lipgloss.Width(s); fill > 0 {
		return s + strings.Repeat(" ", fill)
	}
	return s
}

// Section labels a block within a listing. Faint and uppercased: it is
// scaffolding, and should read as quieter than everything it contains.
func Section(s string) string { return sectionStyle.Render(strings.ToUpper(s)) }

// ErrorLabel is the "Error:" prefix. Red, so a failure is findable in a
// scrollback without reading.
func ErrorLabel() string { return failStyle.Render("Error:") }

// Count is a secondary quantity beside a name — "5 repos · 3 workflows".
func Count(s string) string { return countStyle.Render(s) }

// Heading names a group or a section.
func Heading(s string) string { return headingStyle.Render(s) }

// Detail is secondary text: paths, values, counts.
func Detail(s string) string { return detailStyle.Render(s) }

// Hint points at the next command to run.
func Hint(s string) string { return hintStyle.Render(s) }

// Alert is for text that should stop someone.
func Alert(s string) string { return alertStyle.Render(s) }

// tagColors fixes the three tags that carry meaning. Red is production
// everywhere in prizm, and that association is worth more than variety.
var tagColors = map[string]lipgloss.TerminalColor{
	"prod":       Red,
	"production": Red,
	"qa":         Yellow,
	"staging":    Yellow,
	"staging":    Yellow,
	"local":      Cyan,
	"dev":        Cyan,
}

// spareTagColors are for tags prizm has no opinion about. Red, yellow and
// cyan are deliberately absent: a custom tag must never be mistaken for a
// production one at a glance.
var spareTagColors = []lipgloss.TerminalColor{
	Green,
	Purple,
	Blue,
	lipgloss.Color("10"), // bright green
	lipgloss.Color("13"), // bright magenta
	lipgloss.Color("12"), // bright blue
}

// TagColor returns a tag's colour, or nil for an empty tag.
//
// Known tags keep their semantic colour. Anything else is hashed to a spare
// one — stable across runs and machines, so a tag you invent looks the same
// every time you see it, and two different tags almost never collide.
func TagColor(tag string) lipgloss.TerminalColor {
	if tag == "" {
		return nil
	}
	if c, ok := tagColors[tag]; ok {
		return c
	}

	h := fnv.New32a()
	_, _ = h.Write([]byte(tag))
	return spareTagColors[h.Sum32()%uint32(len(spareTagColors))]
}

// Tag renders a workflow tag.
//
// Bold coloured text rather than a filled block: the colour still separates
// one tag from another at a glance, and the parentheses keep it legible when
// a scheme washes the hue out. Lighter than a badge, which matters when every
// row in a listing carries one.
func Tag(tag string) string {
	if tag == "" {
		return ""
	}
	return lipgloss.NewStyle().Bold(true).Foreground(TagColor(tag)).Render("(" + tag + ")")
}

// CommandName renders a command or usage line: bold, uncoloured, because it
// is something to type rather than something to react to.
func CommandName(s string) string { return commandStyle.Render(s) }

// Flags restyles a pflag usage block, which arrives as pre-aligned lines of
// the form "  -x, --name type   description". Only the description is dimmed;
// the names stay legible because they are what gets typed.
func Flags(block string) string {
	var out []string

	for _, line := range strings.Split(block, "\n") {
		if strings.TrimSpace(line) == "" {
			out = append(out, line)
			continue
		}

		// Skip pflag's leading indent before looking for the gap, or the
		// indent itself is mistaken for the separator and the whole line
		// reads as description.
		start := len(line) - len(strings.TrimLeft(line, " "))

		gap := strings.Index(line[start:], "  ")
		if gap < 0 {
			out = append(out, commandStyle.Render(line))
			continue
		}

		split := start + gap
		out = append(out, commandStyle.Render(line[:split])+detailStyle.Render(line[split:]))
	}
	return strings.Join(out, "\n")
}

// Path renders a filesystem path for display, abbreviating the user's home
// directory to ~.
//
// Repo paths are stored absolute, which is the right thing for a contract but
// the wrong thing to read: in a listing of four repos under the same home
// directory, the shared prefix is the widest thing on screen and the least
// informative. Only the display is abbreviated — nothing round-trips through
// this.
func Path(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + path[len(home):]
	}
	return path
}
