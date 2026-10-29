package app

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// Preferences and Appearance, as Lody's: settings grouped by what they
// are about, a section heading over each, no group one row.

// conversationFontSizes are the sizes offered, ascending: smaller, small,
// default, large, larger.
var conversationFontSizes = []float32{12, 13, 14, 15, 16}

func fontSizeLabel(size float32) string {
	switch size {
	case 12:
		return locales.FontSizeSmaller()
	case 13:
		return locales.FontSizeSmall()
	case 15:
		return locales.FontSizeLarge()
	case 16:
		return locales.FontSizeLarger()
	}
	return locales.FontSizeDefault()
}

// convFontSize is the conversation's text size.
func (a *App) convFontSize() float32 {
	if a.fontSize == 0 {
		return sizeBody
	}
	return a.fontSize
}

// prefSection is a titled group of settings, a hairline before all but
// the first.
func prefSection(c *ui.Context, p *palette, title string, first bool, rows func()) {
	if !first {
		ui.Box(c).FillWidth().Height(1).Background(p.Border).Margin(14, 0, 6, 0)
	}
	if title != "" {
		ui.Text(c, title).FontSize(sizeSub).FontWeight(600).TextColor(p.Strong).Margin(4, 0, 2, 0)
	}
	ui.Column(c).FillWidth().Gap(14).Children(rows)
}

// switchRow is a setting with a switch.
func (a *App) switchRow(c *ui.Context, p *palette, label, help string, on *bool) {
	settingRow(c.Key(label), p, label, help, func() {
		was := *on
		lodySwitch(c, on, false).Label(label)
		if *on != was {
			a.savePrefs()
		}
	})
}

// selectField is a bordered select, its value and a chevron, listing its
// options under it.
func (a *App) selectField(c *ui.Context, p *palette, key string, icon *ui.SVG, label string, options []option, selected string, pick func(string)) {
	open := a.popover("select/" + key)
	b := ui.ButtonBase(c.Key(key)).Width(220).Height(32).Padding(0, 10).Radius(8).Gap(8).AlignItems(ui.Center).
		Border(1, p.Border).Background(p.Input).Cursor(ui.CursorPointer).Label(label).Transition(colorFade)
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.OnClick(func() { *open = !*open })
	current := ""
	for _, o := range options {
		if o.id == selected {
			current = o.label
		}
	}
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(14, 14).TextColor(p.Text)
		}
		ui.Text(c, current).FontSize(sizeSub).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
		ui.Icon(c, iconChevronDown).Size(14, 14).TextColor(p.Secondary)
	})
	a.optionList(c, p, b, open, options, selected, func(id string) {
		*open = false
		pick(id)
	})
}

func (a *App) preferencesPage(c *ui.Context, p *palette) {
	settingsTitle(c, p, locales.SettingsPreferences())
	prefSection(c, p, locales.PrefConversations(), true, func() {
		settingRow(c, p, locales.SettingsBusy(), locales.SettingsBusyHelp(), func() {
			segmented(c, p, "busy", []option{{"queue", locales.Queue()}, {"steer", locales.Steer()}}, firstNonEmpty(a.busyBehavior, "queue"), func(v string) {
				a.busyBehavior = v
				a.savePrefs()
			})
		})
		a.switchRow(c, p, locales.PrefCodeOnly(), locales.PrefCodeOnlyHelp(), &a.codeOnly)
		a.switchRow(c, p, locales.PrefNotifications(), "", &a.notifications)
	})
	prefSection(c, p, locales.PrefAdvanced(), false, func() {
		a.switchRow(c, p, locales.PrefExperimental(), locales.PrefExperimentalHelp(), &a.experimental)
		settingRow(c, p, locales.PrefClearCache(), locales.PrefClearCacheHelp(), func() {
			outlineButton(c, p, iconTrash, locales.PrefClearCacheButton()).OnClick(func() {
				a.confirm(locales.PrefClearCacheTitle(), locales.PrefClearCacheBody(), locales.PrefClearCacheButton(), a.clearCache)
			})
		})
	})
}

func (a *App) appearancePage(c *ui.Context, p *palette) {
	settingsTitle(c, p, locales.SettingsAppearance())
	prefSection(c, p, "", true, func() {
		settingRow(c, p, locales.SettingsTheme(), "", func() {
			icon := map[string]*ui.SVG{"Light": iconSun, "Dark": iconMoon}[a.appearance]
			if icon == nil {
				icon = iconMonitor
			}
			var options []option
			for i, choice := range appearanceChoices {
				options = append(options, option{choice, appearanceLabels()[i]})
			}
			a.selectField(c, p, "theme", icon, locales.SettingsTheme(), options, firstNonEmpty(a.appearance, "System"), a.setAppearance)
		})
		settingRow(c, p, locales.PrefLanguage(), "", func() {
			options := []option{{"", locales.ThemeSystem()}, {"en", "English"}, {"zh-Hans", "简体中文"}}
			a.selectField(c, p, "language", nil, locales.PrefLanguage(), options, a.language, a.setLanguage)
		})
	})
	prefSection(c, p, locales.PrefText(), false, func() {
		settingRow(c, p, locales.PrefFontSize(), "", func() {
			var options []option
			for _, size := range conversationFontSizes {
				options = append(options, option{fontSizeID(size), fontSizeLabel(size)})
			}
			a.selectField(c, p, "font-size", nil, locales.PrefFontSize(), options, fontSizeID(a.convFontSize()), func(id string) {
				for _, size := range conversationFontSizes {
					if fontSizeID(size) == id {
						a.fontSize = size
					}
				}
				a.savePrefs()
			})
		})
		a.switchRow(c, p, locales.PrefWide(), locales.PrefWideHelp(), &a.wideMode)
		a.switchRow(c, p, locales.PrefInlineMath(), locales.PrefInlineMathHelp(), &a.inlineMath)
		a.switchRow(c, p, locales.PrefExtendedCode(), locales.PrefExtendedCodeHelp(), &a.extendedCode)
		a.switchRow(c, p, locales.PrefLigatures(), locales.PrefLigaturesHelp(), &a.ligatures)
	})
}

func fontSizeID(size float32) string { return strconv.Itoa(int(size)) }

// setLanguage shows the app in a language, "" following the system's.
func (a *App) setLanguage(tag string) {
	a.language = tag
	a.savePrefs()
	if !a.offline {
		mygo.App.SetLocale(tag)
	}
}

// ligatureFeatures are the font features the conversation, code and tool
// output take: none, with ligatures on.
func (a *App) ligatureFeatures() []string {
	if a.ligatures {
		return nil
	}
	return []string{"liga=0", "calt=0"}
}

// clearCache drops what the app keeps of the cloud on disk and in memory
// and syncs the workspace again; the sign-in stays.
func (a *App) clearCache() {
	if matches, err := filepath.Glob(filepath.Join(dataDir(), "catalog-*.json")); err == nil {
		for _, f := range matches {
			os.Remove(f)
		}
	}
	a.images = map[string]*remoteImage{}
	a.mentionSources = nil
	a.fetches = map[string]*fetched{}
	if a.workspaceID != "" && !a.offline {
		a.connect(a.workspaceID)
	}
	a.toast("success", locales.PrefCacheCleared())
}

// notifyTransitions tells the desktop when a session another window or
// view isn't showing finishes or comes to need the user, as Lody's desktop
// notifications do.
func (a *App) notifyTransitions(before, after catalog.Catalog) {
	if !a.notifications || a.offline || a.account == nil {
		return
	}
	was := map[string]*catalog.Session{}
	for i := range before.Sessions {
		was[before.Sessions[i].ID] = &before.Sessions[i]
	}
	focused := a.win != nil && a.win.IsFocused()
	for i := range after.Sessions {
		s := &after.Sessions[i]
		old := was[s.ID]
		if old == nil || s.Archived || s.UserID != "" && s.UserID != a.account.User.ID || focused && a.selected == s.ID {
			continue
		}
		body := ""
		switch {
		case s.Waiting() && !old.Waiting():
			body = locales.NotifyNeedsYou()
		case old.Working() && !s.Working():
			body = locales.NotifyFinished()
		default:
			continue
		}
		if !mygo.NotificationsSupported() {
			return
		}
		id := s.ID
		n := mygo.NewNotification(mygo.NotificationOptions{ID: "session-" + id, Title: firstNonEmpty(s.Title, locales.UntitledSession()), Body: body, Group: id})
		n.OnClick(func() {
			a.update(func() {
				if a.win != nil {
					a.win.Show()
					a.win.Focus()
				}
				a.open(id)
			})
		})
		n.Show()
	}
}
