package app

import (
	"encoding/json"
	"fmt"
	"github.com/lbr77/lody-mygo/locales"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lbr77/lody-mygo/internal/session"
)

const detailLimit = 6000 // characters of output shown inline

// toolDetail is a tool call's sheet, as Lody's expanded tool detail:
// diffs, the command and its output, or the raw input and output.
func (a *App) toolDetail(c *ui.Context, p *palette, key string, it session.Item) {
	ui.Column(c).FillWidth().Margin(2, 0, 6, 10).Radius(10).Border(1, p.Border).Background(p.Composer).Clip().
		Dividers(1, p.Border).Gap(1).Children(func() {
		shown := false
		for i, raw := range it.Content {
			block, _ := raw.(map[string]any)
			if block == nil {
				continue
			}
			switch str(block["type"]) {
			case "diff":
				a.diffSection(c.Key(i), p, fmt.Sprintf("%s/%d", key, i), block)
				shown = true
			case "terminal":
				command := strings.TrimSpace(str(block["command"]) + " " + joinArgs(block["args"]))
				if command != "" || str(block["output"]) != "" {
					terminalSection(c.Key(i), p, command, str(block["output"]), block["exitStatus"])
					shown = true
				}
			case "content":
				if text := str(obj(block["content"])["text"]); text != "" {
					textSection(c.Key(i), p, "", text)
					shown = true
				}
			}
		}
		if shown {
			return
		}
		if it.RawInput != nil {
			textSection(c.Key("in"), p, locales.DetailInput(), pretty(it.RawInput))
		}
		if it.RawOutput != nil {
			textSection(c.Key("out"), p, locales.DetailOutput(), pretty(it.RawOutput))
		}
	})
}

func (a *App) diffSection(c *ui.Context, p *palette, key string, block map[string]any) {
	path := str(block["path"])
	d := a.diffs[key]
	if d == nil {
		lang := editor.LanguageFor(path)
		before, after := editor.NewBuffer(str(block["oldText"])), editor.NewBuffer(str(block["newText"]))
		before.SetLanguage(lang)
		after.SetLanguage(lang)
		d = &editor.DiffState{Old: before, New: after, Layout: editor.DiffUnified, FitContent: true, Wrap: true}
		a.diffs[key] = d
	}
	ui.Column(c).FillWidth().Children(func() {
		if path != "" {
			ui.Text(c, path).Font(fontMono).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine().Padding(8, 12, 6, 12)
		}
		editor.DiffView(c, d).FillWidth()
	})
}

func terminalSection(c *ui.Context, p *palette, command, output string, exit any) {
	ui.Column(c).FillWidth().Padding(8, 12).Gap(6).Children(func() {
		if command != "" {
			ui.Text(c, "$ "+command).Font(fontMono).FontSize(sizeFoot).TextColor(p.Text).Selectable()
		}
		if output != "" {
			ui.Text(c, clip(output)).Font(fontMono).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary).Selectable()
		}
		if code, ok := exit.(map[string]any); ok && code["exitCode"] != nil && code["exitCode"] != 0.0 {
			ui.Text(c, locales.ExitedWith(fmt.Sprint(code["exitCode"]))).FontSize(sizeCaption).TextColor(p.Danger)
		}
	})
}

func textSection(c *ui.Context, p *palette, title, text string) {
	ui.Column(c).FillWidth().Padding(8, 12).Gap(4).Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(sizeCaption).FontWeight(500).TextColor(p.Tertiary)
		}
		ui.Text(c, clip(text)).Font(fontMono).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary).Selectable()
	})
}

func clip(s string) string {
	s = strings.TrimRight(s, "\n")
	if r := []rune(s); len(r) > detailLimit {
		return string(r[:detailLimit]) + "\n…"
	}
	return s
}

func pretty(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func joinArgs(v any) string {
	list, _ := v.([]any)
	parts := make([]string, 0, len(list))
	for _, a := range list {
		parts = append(parts, fmt.Sprint(a))
	}
	return strings.Join(parts, " ")
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
