package ui

import gc "github.com/FlameInTheDark/go-gpui/color"

// Theme variants. The default is Catppuccin Mocha (the editor's original
// palette); the settings panel switches between them live. Every variant
// fills the full Theme so no field falls back to zeroed colors.

// ThemeNames lists the selectable themes, display order.
func ThemeNames() []string {
	return []string{
		"Catppuccin Mocha",
		"Catppuccin Latte",
		"Tokyo Night",
		"Tokyo Night Light",
		"Gruvbox Dark",
		"IntelliJ Dark",
	}
}

// ThemeByName resolves a settings-panel theme name; unknown names fall
// back to the default so a hand-edited settings file can never produce a
// zeroed palette.
func ThemeByName(name string) Theme {
	switch name {
	case "IntelliJ Dark":
		return intellijDarkTheme()
	case "Catppuccin Latte":
		return latteTheme()
	case "Tokyo Night":
		return tokyoNightTheme()
	case "Tokyo Night Light":
		return tokyoNightLightTheme()
	case "Gruvbox Dark":
		return gruvboxTheme()
	default:
		return DefaultTheme()
	}
}

// intellijDarkTheme uses neutral layered surfaces and restrained accents,
// inspired by the new IntelliJ UI. Existing named palettes remain unchanged.
func intellijDarkTheme() Theme {
	return Theme{
		EditorBg: gc.RGB(0x1E1F22).Hsla(), Surface: gc.RGB(0x2B2D30).Hsla(),
		Surface1: gc.RGB(0x393B40).Hsla(), Surface2: gc.RGB(0x4E5157).Hsla(),
		Crust: gc.RGB(0x25262A).Hsla(), Overlay: gc.RGB(0x72767D).Hsla(),
		Text: gc.RGB(0xDFE1E5).Hsla(), Dim: gc.RGB(0xA0A3AA).Hsla(),
		Accent: gc.RGB(0x6C9EFF).Hsla(), Green: gc.RGB(0x8BCF9B).Hsla(),
		Peach: gc.RGB(0xD6A77B).Hsla(), Yellow: gc.RGB(0xE1C276).Hsla(),
		Mauve: gc.RGB(0xB2A1D6).Hsla(), Teal: gc.RGB(0x79C6BA).Hsla(),
		Red: gc.RGB(0xE77C80).Hsla(), Orange: gc.RGB(0xDDA876).Hsla(),
		Sky: gc.RGB(0x7DB9D9).Hsla(), Selection: gc.RGB(0x365880).Hsla().Alpha(0.70),
		Match: gc.RGB(0xC99842).Hsla().Alpha(0.35),
	}
}

// latteTheme is Catppuccin Latte — the light variant of the default.
func latteTheme() Theme {
	return Theme{
		EditorBg:  gc.RGB(0xEFF1F5).Hsla(),
		Surface:   gc.RGB(0xE6E9EF).Hsla(),
		Surface1:  gc.RGB(0xCCD0DA).Hsla(),
		Surface2:  gc.RGB(0xBCC0CC).Hsla(),
		Crust:     gc.RGB(0xDCE0E8).Hsla(),
		Overlay:   gc.RGB(0x8C8FA1).Hsla(),
		Text:      gc.RGB(0x4C4F69).Hsla(),
		Dim:       gc.RGB(0x7C7F93).Hsla(),
		Accent:    gc.RGB(0x1E66F5).Hsla(),
		Green:     gc.RGB(0x40A02B).Hsla(),
		Peach:     gc.RGB(0xFE640B).Hsla(),
		Yellow:    gc.RGB(0xDF8E1D).Hsla(),
		Mauve:     gc.RGB(0x8839EF).Hsla(),
		Teal:      gc.RGB(0x179299).Hsla(),
		Red:       gc.RGB(0xD20F39).Hsla(),
		Orange:    gc.RGB(0xFE640B).Hsla(),
		Sky:       gc.RGB(0x04A5E5).Hsla(),
		Selection: gc.RGB(0x9CA0B0).Hsla().Alpha(0.35),
		Match:     gc.RGB(0xDF8E1D).Hsla().Alpha(0.35),
	}
}

// tokyoNightTheme is the Tokyo Night dark palette.
func tokyoNightTheme() Theme {
	return Theme{
		EditorBg:  gc.RGB(0x1A1B26).Hsla(),
		Surface:   gc.RGB(0x16161E).Hsla(),
		Surface1:  gc.RGB(0x292E42).Hsla(),
		Surface2:  gc.RGB(0x3B4261).Hsla(),
		Crust:     gc.RGB(0x13131A).Hsla(),
		Overlay:   gc.RGB(0x565F89).Hsla(),
		Text:      gc.RGB(0xC0CAF5).Hsla(),
		Dim:       gc.RGB(0x737AA2).Hsla(),
		Accent:    gc.RGB(0x7AA2F7).Hsla(),
		Green:     gc.RGB(0x9ECE6A).Hsla(),
		Peach:     gc.RGB(0xFF9E64).Hsla(),
		Yellow:    gc.RGB(0xE0AF68).Hsla(),
		Mauve:     gc.RGB(0xBB9AF7).Hsla(),
		Teal:      gc.RGB(0x73DACA).Hsla(),
		Red:       gc.RGB(0xF7768E).Hsla(),
		Orange:    gc.RGB(0xFF9E64).Hsla(),
		Sky:       gc.RGB(0x7DCFFF).Hsla(),
		Selection: gc.RGB(0x33467C).Hsla().Alpha(0.60),
		Match:     gc.RGB(0xE0AF68).Hsla().Alpha(0.30),
	}
}

// tokyoNightLightTheme is the Tokyo Day light palette.
func tokyoNightLightTheme() Theme {
	return Theme{
		EditorBg:  gc.RGB(0xE1E2E7).Hsla(),
		Surface:   gc.RGB(0xD5D6DB).Hsla(),
		Surface1:  gc.RGB(0xC4C8DA).Hsla(),
		Surface2:  gc.RGB(0xB6BACE).Hsla(),
		Crust:     gc.RGB(0xD0D3E0).Hsla(),
		Overlay:   gc.RGB(0x9699A3).Hsla(),
		Text:      gc.RGB(0x3760BF).Hsla(),
		Dim:       gc.RGB(0x6172B0).Hsla(),
		Accent:    gc.RGB(0x2E7DE9).Hsla(),
		Green:     gc.RGB(0x485E30).Hsla(),
		Peach:     gc.RGB(0xB15B00).Hsla(),
		Yellow:    gc.RGB(0x8C6C3E).Hsla(),
		Mauve:     gc.RGB(0x7D0B59).Hsla(),
		Teal:      gc.RGB(0x007197).Hsla(),
		Red:       gc.RGB(0xC64343).Hsla(),
		Orange:    gc.RGB(0xB15B00).Hsla(),
		Sky:       gc.RGB(0x007197).Hsla(),
		Selection: gc.RGB(0x8C9CC9).Hsla().Alpha(0.40),
		Match:     gc.RGB(0x8C6C3E).Hsla().Alpha(0.35),
	}
}

// gruvboxTheme is Gruvbox dark.
func gruvboxTheme() Theme {
	return Theme{
		EditorBg:  gc.RGB(0x282828).Hsla(),
		Surface:   gc.RGB(0x1D2021).Hsla(),
		Surface1:  gc.RGB(0x3C3836).Hsla(),
		Surface2:  gc.RGB(0x504945).Hsla(),
		Crust:     gc.RGB(0x161819).Hsla(),
		Overlay:   gc.RGB(0x928374).Hsla(),
		Text:      gc.RGB(0xEBDBB2).Hsla(),
		Dim:       gc.RGB(0xA89984).Hsla(),
		Accent:    gc.RGB(0x83A598).Hsla(),
		Green:     gc.RGB(0xB8BB26).Hsla(),
		Peach:     gc.RGB(0xFE8019).Hsla(),
		Yellow:    gc.RGB(0xFABD2F).Hsla(),
		Mauve:     gc.RGB(0xD3869B).Hsla(),
		Teal:      gc.RGB(0x8EC07C).Hsla(),
		Red:       gc.RGB(0xFB4934).Hsla(),
		Orange:    gc.RGB(0xFE8019).Hsla(),
		Sky:       gc.RGB(0x83A598).Hsla(),
		Selection: gc.RGB(0x504945).Hsla().Alpha(0.60),
		Match:     gc.RGB(0xFABD2F).Hsla().Alpha(0.30),
	}
}
