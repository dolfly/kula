package tui

import "github.com/charmbracelet/lipgloss"

// Palette — dark purple/slate theme inspired by Charmbracelet's aesthetic.
var (
	clrPrimary  = lipgloss.Color("#C084FC") // violet-400
	clrSky      = lipgloss.Color("#38BDF8") // sky-400
	clrGreen    = lipgloss.Color("#4ADE80") // green-400
	clrYellow   = lipgloss.Color("#FBBF24") // amber-400
	clrRed      = lipgloss.Color("#F87171") // red-400
	clrBg       = lipgloss.Color("#0D0E1A")
	clrSurface  = lipgloss.Color("#141527")
	clrCard     = lipgloss.Color("#1A1B2E")
	clrBorder   = lipgloss.Color("#2D2E48")
	clrText     = lipgloss.Color("#E2E8F0")
	clrSubtext  = lipgloss.Color("#94A3B8")
	clrDim      = lipgloss.Color("#475569")
)

// ── Header ───────────────────────────────────────────────────────────────────

var (
	sHeaderBg = lipgloss.NewStyle().Background(clrSurface)
	sLogo     = lipgloss.NewStyle().
			Background(clrPrimary).
			Foreground(clrBg).
			Bold(true).
			Padding(0, 1)
	sHeaderPipe = lipgloss.NewStyle().
			Background(clrSurface).
			Foreground(clrBorder)
	sHeaderKey = lipgloss.NewStyle().
			Background(clrSurface).
			Foreground(clrSubtext)
	sHeaderVal = lipgloss.NewStyle().
			Background(clrSurface).
			Foreground(clrText).
			Bold(true)
	sHeaderTime = lipgloss.NewStyle().
			Background(clrSurface).
			Foreground(clrPrimary).
			Bold(true)
)

// ── Tab bar ──────────────────────────────────────────────────────────────────

var (
	sTabBarBg = lipgloss.NewStyle().Background(clrSurface)
	sTabAct   = lipgloss.NewStyle().
			Background(clrPrimary).
			Foreground(clrBg).
			Bold(true).
			Padding(0, 2)
	sTabInact = lipgloss.NewStyle().
			Background(clrSurface).
			Foreground(clrSubtext).
			Padding(0, 2)
	sTabNum = lipgloss.NewStyle().
		Background(clrSurface).
		Foreground(clrDim)
	sTabNumAct = lipgloss.NewStyle().
			Background(clrPrimary).
			Foreground(clrBg)
)

// ── Footer ───────────────────────────────────────────────────────────────────

var (
	sFooterBg = lipgloss.NewStyle().Background(clrSurface)
	sFooterKey = lipgloss.NewStyle().
			Background(clrPrimary).
			Foreground(clrBg).
			Padding(0, 1)
	sFooterHint = lipgloss.NewStyle().
			Background(clrSurface).
			Foreground(clrSubtext)
	sFooterSep = lipgloss.NewStyle().
			Background(clrSurface).
			Foreground(clrBorder)
)

// ── Content ──────────────────────────────────────────────────────────────────

var sContent = lipgloss.NewStyle().Background(clrBg)

// ── Panels ───────────────────────────────────────────────────────────────────

var (
	sPanel = lipgloss.NewStyle().
		Background(clrCard).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(clrBorder).
		Padding(1, 2)
	sPanelTitle = lipgloss.NewStyle().
			Foreground(clrSky).
			Bold(true)
	sPanelTitleAlt = lipgloss.NewStyle().
			Foreground(clrPrimary).
			Bold(true)
	sDivider = lipgloss.NewStyle().Foreground(clrBorder)
)

// ── Table / Labels / Values ───────────────────────────────────────────────────

var (
	sTH    = lipgloss.NewStyle().Foreground(clrSubtext).Bold(true)
	sTD    = lipgloss.NewStyle().Foreground(clrText)
	sTDDim = lipgloss.NewStyle().Foreground(clrSubtext)

	sLabel = lipgloss.NewStyle().Foreground(clrSubtext)
	sValue = lipgloss.NewStyle().Foreground(clrText).Bold(true)
	sGood  = lipgloss.NewStyle().Foreground(clrGreen).Bold(true)
	sWarn  = lipgloss.NewStyle().Foreground(clrYellow).Bold(true)
	sCrit  = lipgloss.NewStyle().Foreground(clrRed).Bold(true)
	sMuted = lipgloss.NewStyle().Foreground(clrDim)
)

// ── Progress bars ────────────────────────────────────────────────────────────

var (
	sBarGood  = lipgloss.NewStyle().Foreground(clrGreen)
	sBarWarn  = lipgloss.NewStyle().Foreground(clrYellow)
	sBarCrit  = lipgloss.NewStyle().Foreground(clrRed)
	sBarEmpty = lipgloss.NewStyle().Foreground(clrDim)
)
