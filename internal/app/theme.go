package app

import "github.com/egoist/mygo/ui"

// palette holds Lody's design tokens: Lody Light, and Vesper with Lody's
// deep-sea remap in dark mode, as the desktop app derives them from its
// bundled VS Code themes.
type palette struct {
	Dark bool

	Ground     ui.Color // window ground, behind the sidebar
	Canvas     ui.Color // the workspace card
	Popover    ui.Color
	Composer   ui.Color
	Input      ui.Color
	Hover      ui.Color
	Selection  ui.Color
	SideHover  ui.Color
	SideSelect ui.Color

	Border         ui.Color
	CardRim        ui.Color
	ComposerBorder ui.Color

	Text        ui.Color // interface text
	Reading     ui.Color // conversation prose
	Strong      ui.Color
	Secondary   ui.Color
	Tertiary    ui.Color
	SideRow     ui.Color // sidebar session titles
	SideRowSel  ui.Color
	SideMuted   ui.Color
	GroupLabel  ui.Color
	Placeholder ui.Color

	Primary    ui.Color
	OnPrimary  ui.Color
	Link       ui.Color
	UserBubble ui.Color
	CodeBg     ui.Color
	InlineCode ui.Color

	Success, Warning, Danger ui.Color
	DiffAdd, DiffDel         ui.Color
}

var lightPalette = palette{
	Ground:         ui.Hex("#F0F0F0"),
	Canvas:         ui.Hex("#F9F9F9"),
	Popover:        ui.Hex("#FFFFFF"),
	Composer:       ui.Hex("#FFFFFF"),
	Input:          ui.Hex("#E0E0E0"),
	Hover:          ui.Hex("#EFEFEF"),
	Selection:      ui.Hex("#D6D6D6"),
	SideHover:      ui.Hex("#E2E2E2"),
	SideSelect:     ui.Hex("#D6D6D6"),
	Border:         ui.Hex("#DADADA"),
	CardRim:        ui.Hex("#E3E3E3"),
	ComposerBorder: ui.Hex("#E8E8E8"),
	Text:           ui.Hex("#1C1C1C"),
	Reading:        ui.Hex("#1E1E1E"),
	Strong:         ui.Hex("#1C1C1C"),
	Secondary:      ui.Hex("#767676"),
	Tertiary:       ui.Hex("#9B9B9B"),
	SideRow:        ui.Hex("#3A3A3A"),
	SideRowSel:     ui.Hex("#1C1C1C"),
	SideMuted:      ui.Hex("#6B6B6B"),
	GroupLabel:     ui.Hex("#868686"),
	Placeholder:    ui.Hex("#939393"),
	Primary:        ui.Hex("#5B8DEF"),
	OnPrimary:      ui.Hex("#FFFFFF"),
	Link:           ui.Hex("#186C95"),
	UserBubble:     ui.Hex("#EEEEEE"),
	CodeBg:         ui.Hex("#F0F0F0"),
	InlineCode:     ui.Hex("#EBEBEB"),
	Success:        ui.Hex("#1A7F37"),
	Warning:        ui.Hex("#A65E2B"),
	Danger:         ui.Hex("#CF222E"),
	DiffAdd:        ui.Hex("#1A7F37"),
	DiffDel:        ui.Hex("#CE222D"),
}

var darkPalette = palette{
	Dark:           true,
	Ground:         ui.Hex("#191A1D"),
	Canvas:         ui.Hex("#131416"),
	Popover:        ui.Hex("#1D1F22"),
	Composer:       ui.Hex("#1D1F22"),
	Input:          ui.Hex("#1E2023"),
	Hover:          ui.Hex("#1C1D1F"),
	Selection:      ui.Hex("#25272B"),
	SideHover:      ui.Hex("#2B2D31"),
	SideSelect:     ui.Hex("#252E35"),
	Border:         ui.Hex("#2B2D31"),
	CardRim:        ui.Hex("#242529"),
	ComposerBorder: ui.Hex("#27292D"),
	Text:           ui.Hex("#D7D8D9"),
	Reading:        ui.Hex("#E4E5E7"),
	Strong:         ui.Hex("#F0F1F2"),
	Secondary:      ui.Hex("#9C9FA3"),
	Tertiary:       ui.Hex("#6F7175"),
	SideRow:        ui.Hex("#BCBEC2"),
	SideRowSel:     ui.Hex("#E4E5E7"),
	SideMuted:      ui.Hex("#9C9FA3"),
	GroupLabel:     ui.Hex("#828488"),
	Placeholder:    ui.Hex("#9C9FA3"),
	Primary:        ui.Hex("#7CC4E8"),
	OnPrimary:      ui.Hex("#000000"),
	Link:           ui.Hex("#7EC4E7"),
	UserBubble:     ui.Hex("#1D1E20"),
	CodeBg:         ui.Hex("#1D1F22"),
	InlineCode:     ui.Hex("#1F2023"),
	Success:        ui.Hex("#3BCE87"),
	Warning:        ui.Hex("#FFC799"),
	Danger:         ui.Hex("#FF8080"),
	DiffAdd:        ui.Hex("#40BA50"),
	DiffDel:        ui.Hex("#F85249"),
}

// Typography, at Lody's default 14px interface size.
const (
	fontUI      = "Inter, system-ui"
	fontMono    = "JetBrains Mono, SF Mono, monospace"
	sizeCaption = 11
	sizeFoot    = 12
	sizeSub     = 13
	sizeBody    = 14
	sizeTitle   = 18
)

// applyTheme sets the window theme from the palette, keeping the system's
// accessibility adjustments, and returns the palette for the frame.
func applyTheme(c *ui.Context) *palette {
	t := *c.Theme()
	p := &lightPalette
	if t.Dark {
		p = &darkPalette
	}
	t.Background = p.Canvas
	t.Surface = p.Popover
	t.SurfaceHover = p.Hover
	t.SurfacePressed = p.Selection
	t.Border = p.Border
	t.Text = p.Text
	t.TextMuted = p.Secondary
	t.Accent = p.Text // Lody's primary buttons are ink, not accent
	t.AccentHover = p.Text.Alpha(0.88)
	t.AccentPressed = p.Text.Alpha(0.8)
	t.AccentText = p.Canvas
	t.Danger = p.Danger
	t.Warning = p.Warning
	t.Success = p.Success
	t.Selection = p.Primary.Alpha(0.25)
	t.Focus = p.Primary
	t.Radius = 8
	t.FontSize = sizeBody
	t.Font = fontUI
	c.SetTheme(&t)
	return p
}
