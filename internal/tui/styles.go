package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// Color palette - bound to terminal ANSI slots so the app follows the user's shell theme.
var (
	createColor   lipgloss.Color
	destroyColor  lipgloss.Color
	updateColor   lipgloss.Color
	replaceColor  lipgloss.Color
	readColor     lipgloss.Color
	selectedBg    lipgloss.Color
	headerColor   lipgloss.Color
	mutedColorVal lipgloss.Color
	textColor     lipgloss.Color
	computedColor lipgloss.Color
)

// ANSI 16-color slot assignments. Terminals remap these per theme, so the app
// inherits whatever palette the user has set in their shell.
var ansiPalette = map[string]string{
	"green":   "2",  // create
	"red":     "1",  // destroy
	"yellow":  "3",  // update
	"magenta": "5",  // replace
	"cyan":    "6",  // read / computed
	"blue":    "4",  // header / info
	"gray":    "8",  // muted / selection bg (bright black)
	"text":    "",   // inherit terminal default foreground
}

// IsLightBackground is retained for backward compatibility. With ANSI-bound
// colors the terminal owns the palette, so this no longer drives styling.
func IsLightBackground() bool {
	return false
}

func init() {
	InitColors()
}

// InitColors assigns the ANSI palette and (re)builds the lipgloss styles.
func InitColors() {
	createColor = lipgloss.Color(ansiPalette["green"])
	destroyColor = lipgloss.Color(ansiPalette["red"])
	updateColor = lipgloss.Color(ansiPalette["yellow"])
	replaceColor = lipgloss.Color(ansiPalette["magenta"])
	readColor = lipgloss.Color(ansiPalette["cyan"])
	headerColor = lipgloss.Color(ansiPalette["blue"])
	mutedColorVal = lipgloss.Color(ansiPalette["gray"])
	selectedBg = lipgloss.Color(ansiPalette["gray"])
	textColor = lipgloss.Color(ansiPalette["text"])
	computedColor = lipgloss.Color(ansiPalette["cyan"])
	initStyles()
}

// SetDarkPalette is kept for backward compatibility. Colors now follow the
// terminal's ANSI palette, so explicit dark/light selection is unnecessary.
func SetDarkPalette() { InitColors() }

// SetLightPalette is kept for backward compatibility. See SetDarkPalette.
func SetLightPalette() { InitColors() }

// Styles - initialized after colors are set
var (
	appStyle             lipgloss.Style
	headerStyle          lipgloss.Style
	summaryStyle         lipgloss.Style
	resourceCreateStyle  lipgloss.Style
	resourceDestroyStyle lipgloss.Style
	resourceUpdateStyle  lipgloss.Style
	resourceReplaceStyle lipgloss.Style
	resourceReadStyle    lipgloss.Style
	attrNameStyle        lipgloss.Style
	attrOldValueStyle    lipgloss.Style
	attrNewValueStyle    lipgloss.Style
	attrComputedStyle    lipgloss.Style
	mutedColor           lipgloss.Style
	helpStyle            lipgloss.Style
	searchStyle          lipgloss.Style
	matchStyle           lipgloss.Style
)

// Action symbols - set after colors
var (
	createSymbol       string
	destroySymbol      string
	updateSymbol       string
	replaceSymbol      string
	readSymbol         string
	forgetSymbol       string
	expandedIndicator  string
	collapsedIndicator string
)

func initStyles() {
	// App container
	appStyle = lipgloss.NewStyle().
		Padding(1, 2)

	// Header
	headerStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(headerColor).
		MarginBottom(1)

	// Summary line
	summaryStyle = lipgloss.NewStyle().
		Foreground(textColor).
		MarginBottom(1)

	// Resource styles based on action
	resourceCreateStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(createColor)

	resourceDestroyStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(destroyColor)

	resourceUpdateStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(updateColor)

	resourceReplaceStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(replaceColor)

	resourceReadStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(readColor)

	// Attribute styles
	attrNameStyle = lipgloss.NewStyle().
		Foreground(textColor)

	attrOldValueStyle = lipgloss.NewStyle().
		Foreground(destroyColor).
		Strikethrough(true)

	attrNewValueStyle = lipgloss.NewStyle().
		Foreground(createColor)

	attrComputedStyle = lipgloss.NewStyle().
		Foreground(computedColor).
		Italic(true)

	// Muted style for general muted text
	mutedColor = lipgloss.NewStyle().
		Foreground(mutedColorVal)

	// Action symbols
	createSymbol = lipgloss.NewStyle().Foreground(createColor).Render("+")
	destroySymbol = lipgloss.NewStyle().Foreground(destroyColor).Render("-")
	updateSymbol = lipgloss.NewStyle().Foreground(updateColor).Render("~")
	replaceSymbol = lipgloss.NewStyle().Foreground(replaceColor).Render("±")
	readSymbol = lipgloss.NewStyle().Foreground(readColor).Render("≤")
	forgetSymbol = lipgloss.NewStyle().Foreground(mutedColorVal).Render("⊘")

	// Expand/collapse indicators
	expandedIndicator = lipgloss.NewStyle().Foreground(mutedColorVal).Render("▼")
	collapsedIndicator = lipgloss.NewStyle().Foreground(mutedColorVal).Render("▶")

	// Help style
	helpStyle = lipgloss.NewStyle().
		Foreground(mutedColorVal).
		MarginTop(1)

	// Search style
	searchStyle = lipgloss.NewStyle().
		Foreground(headerColor).
		Bold(true)

	// Match highlight
	matchStyle = lipgloss.NewStyle().
		Background(selectedBg).
		Foreground(createColor).
		Bold(true)
}

// GetActionSymbol returns the appropriate symbol for an action
func GetActionSymbol(action string) string {
	switch action {
	case "create":
		return createSymbol
	case "delete":
		return destroySymbol
	case "update":
		return updateSymbol
	case "replace":
		return replaceSymbol
	case "read":
		return readSymbol
	case "forget":
		return forgetSymbol
	case "output":
		return updateSymbol
	default:
		return updateSymbol
	}
}

// GetResourceStyle returns the appropriate style for a resource action
func GetResourceStyle(action string) lipgloss.Style {
	switch action {
	case "create":
		return resourceCreateStyle
	case "delete":
		return resourceDestroyStyle
	case "update":
		return resourceUpdateStyle
	case "replace":
		return resourceReplaceStyle
	case "read", "forget":
		return resourceReadStyle
	case "output":
		return resourceUpdateStyle
	default:
		return resourceUpdateStyle
	}
}

// GetActionColor returns the color for an action type
func GetActionColor(action string) lipgloss.Color {
	switch action {
	case "create":
		return createColor
	case "delete":
		return destroyColor
	case "update":
		return updateColor
	case "replace":
		return replaceColor
	case "read", "forget":
		return readColor
	case "output":
		return updateColor
	default:
		return updateColor
	}
}

// FormatStatusColored returns a color-styled status string for CLI output
func FormatStatusColored(status string) string {
	if status == "" {
		return ""
	}

	var style lipgloss.Style
	var label string

	switch status {
	case "success":
		label = "[SUCCESS]"
		style = lipgloss.NewStyle().Foreground(createColor)
	case "failed":
		label = "[FAILED]"
		style = lipgloss.NewStyle().Foreground(destroyColor)
	case "cancelled":
		label = "[CANCELLED]"
		style = lipgloss.NewStyle().Foreground(updateColor)
	case "pending":
		label = "[PENDING]"
		style = lipgloss.NewStyle().Foreground(updateColor)
	case "nochanges":
		return ""
	default:
		return ""
	}

	return style.Render(label)
}

// FormatHistoryEntryColored formats a history entry with colored status for CLI output
func FormatHistoryEntryColored(timestamp, command, status, path string) string {
	// Command with color (pad first, then color)
	cmdPadded := fmt.Sprintf("%-7s", command)
	cmdStyle := lipgloss.NewStyle()
	switch command {
	case "apply":
		cmdStyle = cmdStyle.Foreground(createColor)
	case "destroy":
		cmdStyle = cmdStyle.Foreground(destroyColor)
	case "plan":
		cmdStyle = cmdStyle.Foreground(headerColor)
	}
	cmdColored := cmdStyle.Render(cmdPadded)

	// Status with color (pad the label first, then color)
	var statusColored string
	statusPadded := fmt.Sprintf("%-12s", "") // default empty padding
	switch status {
	case "success":
		statusPadded = fmt.Sprintf("%-12s", "[SUCCESS]")
		statusColored = lipgloss.NewStyle().Foreground(createColor).Render(statusPadded)
	case "failed":
		statusPadded = fmt.Sprintf("%-12s", "[FAILED]")
		statusColored = lipgloss.NewStyle().Foreground(destroyColor).Render(statusPadded)
	case "cancelled":
		statusPadded = fmt.Sprintf("%-12s", "[CANCELLED]")
		statusColored = lipgloss.NewStyle().Foreground(updateColor).Render(statusPadded)
	case "pending":
		statusPadded = fmt.Sprintf("%-12s", "[PENDING]")
		statusColored = lipgloss.NewStyle().Foreground(updateColor).Render(statusPadded)
	default:
		statusColored = statusPadded // no color, just spaces
	}

	// Pad path to 40 chars for consistent line width
	pathPadded := fmt.Sprintf("%-40s", path)

	return fmt.Sprintf("%s  %s  %s  %s",
		timestamp,
		cmdColored,
		statusColored,
		pathPadded,
	)
}
