package workspace

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/FlameInTheDark/go-gpui/app"
	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/font"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
	"github.com/FlameInTheDark/go-gpui/text"

	"github.com/FlameInTheDark/flint/internal/debugger"
	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/terminal"
	"github.com/FlameInTheDark/flint/internal/ui"
)

// Render builds the whole window tree. It runs inside the root view's
// update (see gpui.SetRootView), so closures built here may safely call
// update only from event handlers (after the frame is presented).
func (ws *Workspace) Render(handle *gpui.WindowHandle[Workspace], cx *app.App, w *gpui.Window) gpui.Element {
	// The font size changed in the settings panel: re-measure the mono
	// metrics so the editor, gutter, terminal and cursor math stay exact.
	if ws.measuredFontPx != ws.Cfg.Editor.FontSize {
		ws.measureMetrics(w)
	}
	ws.codeShaper = editor.NewCodeShaper(w, float32(ws.Cfg.Editor.FontSize))
	t := ws.Theme()
	winSize := w.LogicalContentSize()
	if winSize.Width.AsF32() <= 0 {
		winSize = geometry.SizePixels(960, 600)
	}
	ws.WindowSize = winSize
	winW := float32(winSize.Width.AsF32())
	winH := float32(winSize.Height.AsF32())

	update := func(f func(*Workspace)) {
		handle.View.Update(cx, func(state *Workspace, c *app.Context[Workspace]) any {
			f(state)
			c.Notify() // route invalidation through windows (upstream cx.notify)
			return nil
		})
	}
	redraw := func(f func(*Workspace)) {
		update(f)
		w.Refresh()
	}
	focusEditor := func(w *gpui.Window) { w.Focus(ws.FocusEditor) }

	// measureUIWidth measures a string's rendered width in the UI font
	// (menu/popup sizing). Falls back to a rough estimate when the text
	// system is unavailable (headless text-less setups).
	measureUI := func(s string) float32 {
		if sys := w.TextSystem(); sys != nil {
			ts := style.DefaultTextStyle()
			if id, ok := sys.FontID(font.Font{Family: "", Weight: font.WeightNormal}); ok {
				l := sys.LayoutLine(s, ts.FontSize.ToPixels(w.RemSize()),
					[]text.FontRun{{Len: len(s), FontID: id}})
				return l.Width.AsF32()
			}
		}
		return float32(len(s)) * 7.5
	}

	// -----------------------------------------------------------------------
	// Menu bar
	// -----------------------------------------------------------------------
	quit := func() {
		if ws.quit != nil {
			ws.quit()
		}
	}
	menus := MenuDefs(update, w, quit, ws)
	if idx := ws.Menu.Open; ws.Popup != nil && idx >= 0 && idx < len(menus) && menus[idx].Title == "Run" {
		// A process can exit while the Run menu is open. Keep its actions
		// in sync with the toolbar without dismissing the menu.
		ws.Popup.Items = menus[idx].Items()
	}
	openMenu := func(idx int, x float32) {
		update(func(state *Workspace) {
			state.Menu.Open = idx
			state.Menu.X = x
			state.Menu.Hover = -1
			if idx >= 0 && idx < len(menus) {
				state.Popup = &ui.PopupState{
					Items: menus[idx].Items(),
					X:     x,
					// Dropdowns anchor below the menu bar: with
					// client-side decorations the bar rides below
					// the app's title bar, so the origin is the
					// chrome top (title bar + menu bar), not the
					// bare MenuBarHeight.
					Y:     ws.chromeTop(),
					Hover: -1,
				}
			} else {
				state.Popup = nil
			}
		})
		w.Focus(ws.FocusPopup)
	}
	closePopup := func() {
		update(func(state *Workspace) {
			state.Menu.Open = -1
			state.Popup = nil
		})
	}
	openContext := func(items []ui.MenuItem, pos geometry.Point[geometry.Pixels]) {
		update(func(state *Workspace) {
			state.Menu.Open = -1
			state.Popup = &ui.PopupState{
				Items: items,
				X:     float32(pos.X.AsF32()),
				Y:     float32(pos.Y.AsF32()),
				Hover: -1,
			}
		})
		w.Focus(ws.FocusPopup)
	}
	openCommandPalette := func(win *gpui.Window) {
		update(func(s *Workspace) {
			s.closeFileSearch()
			s.CommandPalette = &CommandPaletteState{Sel: 0}
			s.Menu.Open = -1
			s.Popup = nil
		})
		win.Focus(ws.FocusCommand)
		win.Refresh()
	}
	projectName := ""
	if ws.Tree != nil {
		projectName = ws.Tree.Name
	}
	toggleProject := func(win *gpui.Window) {
		if ws.Tree == nil {
			ws.openPicker(PickerOpenFolder)
		} else {
			update(func(s *Workspace) { s.ToggleTree() })
		}
		win.Refresh()
	}

	var editLaunch func(*gpui.Window)
	if ws.Root != "" {
		editLaunch = func(win *gpui.Window) { ws.editLaunch(update, win) }
	}
	var stopGo func(*gpui.Window)
	if ws.goTaskRunning() {
		stopGo = func(win *gpui.Window) { ws.startGoAction(goStop, update, win) }
	}
	var debugSnap debugger.Snapshot
	var stopDebug func(*gpui.Window)
	if session := ws.debug.session; session != nil {
		debugSnap = session.Snapshot()
		if debugSnap.State != debugger.Finished {
			stopDebug = func(win *gpui.Window) {
				session.Stop()
				win.Refresh()
			}
		}
	}
	menuCfg := ui.MenuBarConfig{
		Menus:       menus,
		State:       &ws.Menu,
		Theme:       t,
		Project:     projectName,
		Compact:     winW < 820,
		VeryCompact: winW < 600,
		OnSearch:    func(win *gpui.Window) { ws.openFileSearch(searchFiles, update, win) },
		OnSettings: func(win *gpui.Window) {
			update(func(s *Workspace) { s.OpenSettings() })
			win.Focus(ws.FocusModal)
			win.Refresh()
		},
		OnRun:       func(win *gpui.Window) { ws.startGoAction(goRun, update, win) },
		OnStop:      stopGo,
		OnDebug:     func(win *gpui.Window) { ws.startGoAction(goDebug, update, win) },
		OnStopDebug: stopDebug,
		OnRunConfig: editLaunch,
		RunTarget:   ws.launches.Active,
		OnBuild:     func(win *gpui.Window) { ws.startGoAction(goBuild, update, win) },
		OnTest:      func(win *gpui.Window) { ws.startGoAction(goTest, update, win) },
		OnVet:       func(win *gpui.Window) { ws.startGoAction(goVet, update, win) },
		FPS: func() string {
			if !ws.ShowFPS {
				return ""
			}
			return fmt.Sprintf("%d fps", ws.FPS)
		}(),
		Update:  func(f func(*ui.MenuState)) { update(func(s *Workspace) { f(&s.Menu) }) },
		OnOpen:  openMenu,
		OnClose: closePopup,
	}
	menuBar := ui.RenderMenuBar(menuCfg)

	// -----------------------------------------------------------------------
	// Tab bar
	// -----------------------------------------------------------------------
	// Tabs hug their content and never compress (the upstream editor scrolls the strip
	// instead): with many files open the strip pans horizontally — the
	// wheel over the bar scrolls it, and switching/opening/closing tabs
	// reveals the active one — so a label can never overlap its close
	// button and the buttons stay clickable.
	// The tab strip's scroll handle persists on the workspace: per-render
	// handles would drop every cross-frame request (the element tree
	// rebuilds each frame).
	tabScroll := ws.tabScrollHandle
	revealActiveTab(handle.Window, tabScroll, ws.Active, &ws.tabReveal)
	tabsRow := elements.NewDiv().
		ID(elements.ButtonID("tabs-strip")).
		Flex().ItemsStretch().H(ui.Px(ui.TabBarHeight)).
		Flex1(). // basis 0: fill the bar; wide content scrolls, never inflates
		OverflowXScroll().
		TrackScroll(tabScroll).
		OnScroll(func(ev platform.ScrollEvent, w *gpui.Window) {
			// Vertical wheel pans the strip horizontally (the
			// editors' behavior); horizontal wheel keeps panning
			// through the div's default scroll handling.
			dy := ev.Delta.PixelDelta(geometry.Px(1)).Y.AsF32()
			if dy != 0 {
				tabScroll.ScrollLeftBy(-dy)
				w.Refresh()
			}
		})
	tabBar := elements.NewDiv().ID(ui.StructID("tab-bar")).Flex().ItemsStretch().H(ui.Px(ui.TabBarHeight)).
		FlexShrink(0). // fixed chrome never yields its height to content overflow
		BgColor(t.Crust).BorderB_1().BorderColor(t.Surface1).
		Child(tabsRow)
	for i, b := range ws.Buffers {
		idx := i
		buf := b
		active := i == ws.Active

		labelColor := t.Dim
		tabBg := t.Crust
		if active {
			labelColor = t.Text
			tabBg = t.EditorBg
		}

		tab := elements.NewDiv().
			ID(elements.ButtonID("tab-" + buf.Name)).
			Flex().ItemsCenter().Px(ui.DefPx(10)).Gap(ui.DefPx(2)).TextSize(ui.AbsPx(13)).
			FlexShrink(0). // a tab hugs its content; the strip scrolls instead
			// Tab labels must never wrap: the tab hugs its content,
			// so a rounding hair could wrap the last glyph onto a
			// phantom second row inside the fixed-height bar.
			WhitespaceNowrap().
			CursorPointer().
			BgColor(tabBg).
			BorderB_2().BorderColor(t.Crust).
			Hover(func(s *style.StyleRefinement) {
				if !active {
					s.BgColor(t.Surface1)
				}
			}).
			// Selecting a tab acts on mouse-down: the press is
			// handled in the frame it landed on, so a redraw that
			// restructures the tree mid-click (caret blink, output
			// arriving) can never drop it — and it feels instant,
			// like the browser/IDE tab strips.
			OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
				if ev.Button != platform.ButtonLeft {
					return
				}
				redraw(func(state *Workspace) {
					if state.Active != idx {
						state.completion = nil
						state.documentation = nil
						state.languageRequest++
					}
					state.Active = idx
				})
				w.Focus(ws.FocusEditor)
			}).
			OnRightDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
				openContext(TabContextItems(update, buf.Path), ev.Position)
				w.Refresh()
			})
		if active {
			tab = tab.BorderColor(t.Accent)
		}

		tab = tab.Child(ui.FileTypeIcon(buf.Name, t))
		tab = tab.Child(elements.NewDiv().W(ui.Px(6)))
		tab = tab.Child(elements.NewText(buf.Name).WithTextColor(labelColor))
		// An unmistakable unsaved marker stays visible even on inactive tabs.
		if buf.Dirty {
			tab = tab.Child(elements.NewDiv().Ml(ui.Px(4)).W(ui.Px(8)).H(ui.Px(8)).FlexShrink(0).Rounded(ui.AbsPx(4)).BgColor(t.Text))
		}
		close := elements.NewDiv().
			ID(elements.ButtonID("close-" + buf.Name)).
			TooltipBuilder(ui.Tooltip("Close " + buf.Name)).
			Flex().ItemsCenter().JustifyCenter().
			W(ui.Px(18)).H(ui.Px(18)).Rounded(ui.AbsPx(4)).
			CursorPointer().
			// A 12px glyph centers in the 18px box; the default
			// 16px line box is taller than the button, which made
			// the hover highlight look shifted.
			TextSize(ui.AbsPx(12)).
			Child(elements.NewText("x").WithTextColor(t.Overlay)).
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
			OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
				redraw(func(state *Workspace) {
					if state.Active != idx {
						state.Active = idx
					}
					state.CloseEditor()
				})
			})
		tab = tab.Child(elements.NewDiv().W(ui.Px(6))).Child(close)
		tabsRow = tabsRow.Child(tab)
	}
	// Trailing filler keeps few tabs left-aligned inside the strip; when
	// the tabs overflow, it just follows the last tab off-screen.
	tabsRow = tabsRow.Child(elements.NewDiv().FlexGrow(1))
	if b := ws.Buf(); b != nil && b.Kind == editor.KindText && editor.Language(b.Name) == "markdown" {
		preview := ws.markdownPreview[b] != nil && ws.markdownPreview[b].enabled
		label := "Preview"
		if preview {
			label = "Source"
		}
		button := elements.NewDiv().ID(elements.ButtonID("markdown-preview-toggle")).Flex().ItemsCenter().Px(ui.DefPx(12)).Gap(ui.DefPx(6)).CursorPointer().
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).
			OnClick(func(_ gpui.ClickEvent, w *gpui.Window) {
				redraw(func(s *Workspace) { s.toggleMarkdownPreview(b) })
				w.Focus(ws.FocusEditor)
			}).
			Child(elements.NewText("◧").WithTextColor(t.Accent)).Child(elements.NewText(label).WithTextColor(t.Text))
		tabBar = tabBar.Child(button)
	}
	if ws.StatusError != "" {
		tabBar = tabBar.Child(elements.NewDiv().Flex().ItemsCenter().Px(ui.DefPx(10)).
			Child(elements.NewText(ws.StatusError).WithTextColor(t.Red)))
	}

	// -----------------------------------------------------------------------
	// Editor pane
	// -----------------------------------------------------------------------
	buf := ws.Buf()
	// The editor lives inside an overflow-hidden, flex-filling wrapper:
	// the pane's own content (visible code rows, gutter) must never
	// contribute its height to the workspace row's automatic minimum —
	// otherwise a tall buffer pushes the whole row past the status bar
	// and the app content scrolls out of the window.
	// basis 0 on every flex-filling wrapper (editorArea, editorCol,
	// mainRow, tabsRow, the pane, the code column, the tree list): their
	// flex basis is then the AVAILABLE space, not their content. A
	// content-sized basis let a tall subtree (a big project tree, a long
	// buffer) inflate the workspace row's flex basis; the shrink phase
	// then collapsed the only shrinkable fixed bar (the menu bar) and its
	// text was clipped by the tab strip.
	editorArea := elements.NewDiv().Flex().FlexBasis(ui.Px(0)).FlexGrow(1).FlexShrink(1).
		OverflowHidden().BgColor(t.EditorBg)
	if buf != nil {
		if state := ws.markdownPreview[buf]; state != nil && state.enabled && buf.Kind == editor.KindText && editor.Language(buf.Name) == "markdown" {
			if state.view == nil {
				state.view = newMarkdownView(buf.Text())
				state.revision = buf.Revision
				w.OnNextFrame(func(nw *gpui.Window) { nw.Refresh() })
			} else if state.revision != buf.Revision {
				state.view.setSource(buf.Text())
				state.revision = buf.Revision
			}
			state.view.measureCode = func(s string) float32 { return measureTextWidth(w, s, true) }
			previewW := winW - ws.paneLeft()
			previewW = max(float32(1), previewW)
			documentW := max(float32(1), min(float32(900), previewW-72))
			content := elements.NewDiv().Flex().FlexCol().ItemsCenter().W(ui.Px(previewW)).MinW_0().Py(ui.DefPx(24)).Child(renderMarkdownBlocks(state.view.root, state.view.source, t, true, buf.Path, markdownLinkHandler(ws, buf.Path, update, w), documentW, state.view))
			editorArea.Child(markdownScroll(state.view, content, t, "markdown-preview", ws.FocusEditor, func() { update(func(s *Workspace) { s.Save() }) }))
		} else {
			switch buf.Kind {
			case editor.KindDiff:
				editorArea.Child(renderGitDiff(ws, buf, t, update))
			case editor.KindHex:
				// Binary files open as a read-only hex dump (opening big
				// binaries as text was the app-killer).
				editorArea.Child(editor.RenderHexPane(editor.HexConfig{
					Buf:         buf,
					Theme:       t,
					CharAdvance: ws.CharAdvance,
					LineHeight:  ws.LineHeight,
					Scroll:      ws.scroll(),
					ScrollF:     ws.scrollF(),
					ViewportH:   ws.paneHeight(),
					ViewportW:   ws.paneWidth(),
					FontSizePx:  float32(ws.Cfg.Editor.FontSize),
					Focus:       ws.FocusEditor,
					OnWheelScroll: func(deltaF float32) {
						update(func(state *Workspace) { state.addScrollDelta(deltaF) })
					},
					OnScroll: func(delta int) {
						update(func(state *Workspace) { state.setScroll(state.scroll() + delta) })
					},
				}))
			case editor.KindImage:
				// Supported images open in the zoomable/pannable viewer.
				idx := ws.Active
				editorArea.Child(editor.RenderImagePane(editor.ImageConfig{
					Buf:       buf,
					Theme:     t,
					ViewportW: ws.paneWidth(),
					ViewportH: ws.paneHeight(),
					Zoom:      ws.imgZoom(idx),
					PanX:      ws.imgPan(idx)[0],
					PanY:      ws.imgPan(idx)[1],
					Fit:       ws.imgFit(idx),
					Focus:     ws.FocusEditor,
					OnZoomAt: func(zoom, panX, panY float32) {
						update(func(state *Workspace) {
							state.setImgZoom(zoom)
							state.setImgPan(panX, panY)
							state.setImgFit(false)
						})
					},
					OnDragStart: func(x, y float32) {
						update(func(state *Workspace) { state.imgDragX, state.imgDragY = x, y })
					},
					OnDragMove: func(x, y float32) {
						update(func(state *Workspace) {
							dx, dy := x-state.imgDragX, y-state.imgDragY
							state.imgDragX, state.imgDragY = x, y
							state.setImgPan(state.imgPan(state.Active)[0]+dx, state.imgPan(state.Active)[1]+dy)
							state.setImgFit(false)
						})
					},
					OnPan: func(dx, dy float32) {
						update(func(state *Workspace) {
							state.setImgPan(state.imgPan(state.Active)[0]+dx, state.imgPan(state.Active)[1]+dy)
							state.setImgFit(false)
						})
					},
					OnToggleFit: func() {
						update(func(state *Workspace) { state.toggleImgFit() })
					},
				}))
			default:
				marks, execution := ws.debugMarkers(buf, debugSnap)
				var toggleBreakpoint func(int)
				if canBreakpoint(buf) {
					toggleBreakpoint = func(line int) {
						update(func(s *Workspace) { s.toggleBreakpoint(buf, line) })
						w.Focus(ws.FocusEditor)
					}
				}
				_, caretLineWidth := ws.codeShaper.CaretMetrics(buf, t)
				editorArea.Child(editor.RenderPane(editor.PaneConfig{
					Buf:            buf,
					Theme:          t,
					Metrics:        editor.Metrics{CharAdvance: ws.CharAdvance, LineHeight: ws.LineHeight},
					Shaper:         ws.codeShaper,
					CaretLineWidth: caretLineWidth,
					Scroll:         ws.scroll(),
					ScrollF:        ws.scrollF(),
					ScrollX:        ws.scrollX(),
					ViewportH:      ws.paneHeight(),
					ViewportW:      ws.paneWidth(),
					ShowGutter:     ws.ShowLineNumbers,
					Breakpoints:    marks,
					ExecutionLine:  execution,
					OnBreakpoint:   toggleBreakpoint,
					ShowMinimap:    ws.Cfg.Editor.ShowMinimap,
					FontSizePx:     float32(ws.Cfg.Editor.FontSize),
					Find:           ws.Find,
					Focus:          ws.FocusEditor,
					CaretVisible:   ws.CaretVisible,
					OnScrollX: func(dx float32) {
						update(func(state *Workspace) { state.addScrollX(dx) })
					},
					OnEdit: func(f func(*editor.Buffer)) {
						update(func(state *Workspace) {
							state.NoteCaretActivity()
							if b := state.Buf(); b != nil {
								revision, cursor := b.Revision, b.Cursor
								f(b)
								state.changedLanguage(b, revision, cursor)
								// Keep the caret visible when edits
								// or cursor moves run past the
								// right edge (horizontal pan).
								state.scrollCaretIntoViewX()
							}
						})
					},
					OnKey: func(ev platform.KeyDownEvent) bool {
						k := ev.Keystroke
						if (k.Modifiers.Control || k.Modifiers.Platform) && !k.Modifiers.Alt && !k.Modifiers.Shift && k.Key == "q" {
							update(func(s *Workspace) { s.showCaretDocumentation() })
							return true
						}
						if (k.Modifiers.Control || k.Modifiers.Platform) && (k.Key == "space" || k.Key == " ") {
							update(func(s *Workspace) {
								if b := s.Buf(); b != nil {
									s.completion = nil
									s.documentation = nil
									s.requestLanguage(b, "completion", b.Cursor, 0, 0)
								}
							})
							return true
						}
						if k.Key == "escape" {
							visible := ws.completion != nil || ws.documentation != nil
							update(func(s *Workspace) { s.completion = nil; s.documentation = nil; s.languageRequest++ })
							return visible
						}
						if c := ws.completion; c != nil && c.buffer == ws.Buf() {
							switch k.Key {
							case "up", "down":
								update(func(s *Workspace) {
									if s.completion != nil {
										delta := 1
										if k.Key == "up" {
											delta = -1
										}
										n := len(s.completion.items)
										s.completion.selected = (s.completion.selected + delta + n) % n
									}
								})
								return true
							case "enter", "tab":
								update(func(s *Workspace) {
									if s.completion != nil {
										s.acceptCompletion(s.completion.selected)
									}
								})
								return true
							}
						}
						return false
					},
					OnDefinition: func(offset int) {
						update(func(s *Workspace) {
							if b := s.Buf(); b != nil {
								s.completion = nil
								s.documentation = nil
								s.requestLanguage(b, "definition", offset, 0, 0)
							}
						})
					},
					OnScroll: func(delta int) {
						update(func(state *Workspace) { state.setScroll(state.scroll() + delta) })
					},
					OnScrollTo: func(line int) {
						update(func(state *Workspace) { state.setScroll(line) })
					},
					OnWheelScroll: func(deltaF float32) {
						update(func(state *Workspace) { state.addScrollDelta(deltaF) })
					},
					OnContext: func(pos geometry.Point[geometry.Pixels]) {
						openContext(EditorContextItems(update, w, ws), pos)
					},
					OnSave: func() { update(func(s *Workspace) { s.Save() }); w.Refresh() },
					OnFind: func() { update(func(s *Workspace) { s.OpenFind() }); w.Focus(ws.FocusFind); w.Refresh() },
					OnTerminalToggle: func() {
						update(func(s *Workspace) { s.ToggleTerminal(w) })
						w.Refresh()
					},
				}))
			}
		}
	} else {
		editorArea.Child(renderEmptyState(ws, t, update, w))
	}

	// Find bar.
	var findBar gpui.Element
	if ws.Find != nil {
		findState := ws.Find
		matchLabel := "no results"
		if n := len(findState.Matches); n > 0 {
			matchLabel = fmt.Sprintf("%d / %d", findState.Current+1, n)
		}
		findBar = renderFindBar(ws, t, update, focusEditor, matchLabel)
	}

	// -----------------------------------------------------------------------
	// Terminal dock
	// -----------------------------------------------------------------------
	var termPanel gpui.Element
	ws.wireTerminalRedraw(w.Draw)
	if ws.ShowTerminal && ws.Term != nil {
		tabs := ws.terminalTabs()
		tabCallbacks := make([]terminal.Tab, len(tabs))
		for i, tab := range tabs {
			tabCallbacks[i] = terminal.Tab{Title: tab.Title, Active: tab.Active}
		}
		termPanel = terminal.RenderPane(terminal.PaneConfig{
			Term:        ws.Term,
			Theme:       t,
			Height:      ws.effectiveTermHeight(),
			CharAdvance: ws.CharAdvance,
			LineHeight:  ws.LineHeight,
			BodyWidth:   ws.paneWidth(),
			FontSizePx:  float32(ws.Cfg.Editor.FontSize),
			Focus:       ws.FocusTerminal,
			Focused:     w.Focused() == ws.FocusTerminal,
			Tabs:        tabCallbacks,
			OnSelectTab: func(i int) {
				update(func(s *Workspace) { s.SelectTerminal(i) })
				w.Focus(ws.FocusTerminal)
				w.Refresh()
			},
			OnNewTab: func() {
				update(func(s *Workspace) { s.NewTerminal() })
				w.Focus(ws.FocusTerminal)
				w.Refresh()
			},
			OnCloseTab: func(i int) {
				update(func(s *Workspace) { s.CloseTerminal(i) })
				w.Refresh()
			},
			Update: func(f func(*terminal.Terminal)) {
				ws.Term.Apply(f)
				w.Refresh()
			},
			// The dock's top edge follows the pointer while the
			// user drags the strip/header (the upstream editor's dock resize).
			OnResize: func(y float32) {
				update(func(s *Workspace) { s.SetTermHeight(y) })
				w.Refresh()
			},
			OnClear: func() {
				ws.Term.Apply(func(term *terminal.Terminal) { term.Clear() })
				w.Refresh()
			},
			OnTerminalToggle: func() {
				update(func(s *Workspace) { s.ToggleTerminal(w) })
				w.Refresh()
			},
		})
	}

	// -----------------------------------------------------------------------
	// Project panel
	// -----------------------------------------------------------------------
	var treePanel gpui.Element
	if ws.tools.left == "" && ws.ShowTree && ws.Tree != nil {
		activePath := ""
		if b := ws.Buf(); b != nil {
			activePath = b.Path
		}
		// Label widths are measured once per name (names are static
		// within a session): the rows' definite widths drive the
		// panel's horizontal scroll extent.
		if ws.treeTextW == nil {
			ws.treeTextW = map[string]float32{}
		}
		measureTree := func(s string) float32 {
			if v, ok := ws.treeTextW[s]; ok {
				return v
			}
			v := measureTextWidth(w, s, false)
			ws.treeTextW[s] = v
			return v
		}
		treePanel = RenderTree(ws.Tree, t, activePath, ws.effectiveTreeWidth(), TreeCallbacks{
			Toggle: func(node *TreeNode) {
				update(func(state *Workspace) {
					node.Expanded = !node.Expanded
					if node.Expanded {
						node.EnsureLoaded()
					}
				})
			},
			OpenFile: func(path string) {
				redraw(func(state *Workspace) { state.OpenFile(path) })
				w.Focus(ws.FocusEditor)
			},
			Context: func(node *TreeNode, pos geometry.Point[geometry.Pixels]) {
				openContext(TreeContextItems(update, ws, node), pos)
			},
			Measure: measureTree,
			Scroll:  ws.treeScrollHandle,
			Refresh: func() {
				update(func(s *Workspace) {
					if s.Tree != nil {
						s.Tree.Reload()
						s.treeTextW = nil
					}
				})
			},
			CollapseAll: func() {
				update(func(s *Workspace) {
					if s.Tree != nil {
						collapseTree(s.Tree)
					}
				})
			},
			// The panel's inner edge follows the pointer while
			// the user drags the grip (the upstream editor's dock resize).
			OnResizeWidth: func(x float32) {
				update(func(s *Workspace) { s.SetTreeWidth(x) })
				w.Refresh()
			},
		})
	}

	// -----------------------------------------------------------------------
	// Status bar
	// -----------------------------------------------------------------------
	if ws.tools.left != "" {
		treePanel = renderLeftTool(ws, t, update)
	}
	if ws.tools.left == "git" && ws.tools.gitRefresh && !ws.tools.gitRefreshQueued && ws.tools.gitOp == nil {
		ws.tools.gitRefreshQueued = true
		w.OnNextFrame(func(win *gpui.Window) {
			update(func(s *Workspace) {
				s.tools.gitRefreshQueued = false
				if s.tools.left == "git" && s.tools.gitRefresh {
					s.startToolLoad("git", update, win)
				}
			})
			win.Refresh()
		})
	}
	status := renderStatusBar(ws, t, update)

	// -----------------------------------------------------------------------
	// Assembly
	// -----------------------------------------------------------------------
	mainRow := elements.NewDiv().ID(ui.StructID("main-row")).Flex().FlexBasis(ui.Px(0)).FlexGrow(1)
	if ws.tools.rail {
		mainRow.Child(renderToolRail(ws, t, update, toggleProject))
	}
	if treePanel != nil {
		mainRow = mainRow.Child(treePanel)
	}
	editorCol := elements.NewDiv().ID(ui.StructID("editor-col")).Flex().FlexCol().FlexBasis(ui.Px(0)).FlexGrow(1).MinW_0().Child(tabBar)
	if findBar != nil {
		editorCol = editorCol.Child(findBar)
	}
	editorCol = editorCol.Child(editorArea)
	if termPanel != nil {
		editorCol = editorCol.Child(termPanel)
	}
	if ws.ShowGoOutput && ws.GoTask != nil {
		editorCol = editorCol.Child(renderGoOutput(ws, t, update, w))
	}
	if ws.ShowProblems {
		editorCol = editorCol.Child(renderProblems(ws, t, update))
	}
	if ws.ShowDebug {
		editorCol = editorCol.Child(renderDebug(ws, debugSnap, t, update, w))
	}
	if ws.ShowTodos {
		editorCol.Child(renderTodos(ws, t, update))
	}
	mainRow = mainRow.Child(editorCol)

	root := elements.NewDiv().ID(ui.StructID("root")).Flex().FlexCol().FlexGrow(1).Relative().BgColor(t.EditorBg)
	// The custom title bar rides above the menu bar when the platform
	// window runs client-side decorations.
	if ws.CustomDecorations {
		root = root.Child(renderTitleBar(ws, t, w))
	}
	root = root.
		Child(menuBar).
		Child(mainRow).
		Child(status)

	// Window zoom shortcuts: the keymap bindings (ctrl-= / ctrl+- / ctrl+0,
	// the upstream editor's defaults) dispatch the editor zoom actions, whose paint-time
	// listeners register below (they must re-register every frame).

	// -----------------------------------------------------------------------
	// Overlays (hit-tested above everything, in order)
	// -----------------------------------------------------------------------
	onZoomActions(w)
	if ws.completion != nil && ws.completion.buffer == ws.Buf() {
		root = root.Child(renderCompletions(ws, t, update, w, winW, winH))
	}
	if ws.documentation != nil && ws.documentation.buffer == ws.Buf() {
		root = root.Child(renderDocumentation(ws, t, update, w, winW, winH))
	}
	onSettingsAction(w, func() {
		update(func(s *Workspace) { s.OpenSettings() })
	})
	onCommandPaletteAction(w, func() { openCommandPalette(w) })
	onFileSearchAction(w, func(mode searchMode) { ws.openFileSearch(mode, update, w) })
	onProjectAndGoActions(w, func() { toggleProject(w) }, func(action goAction) { ws.startGoAction(action, update, w) }, func() { update(func(s *Workspace) { s.toggleProblems() }); w.Refresh() })
	gpui.OnAction(w, func(a debugKeyAction, phase gpui.DispatchPhase, win *gpui.Window) {
		if phase != gpui.BubblePhase || ws.SettingsOpen || ws.Prompt != nil || ws.CommandPalette != nil || ws.FileSearch != nil || ws.Picker != nil || ws.confirm != nil || ws.launchEditor != nil {
			return
		}
		if a.command == debugger.Continue && (debugSnap.State == "" || debugSnap.State == debugger.Finished) {
			ws.startGoAction(goDebug, update, win)
			return
		}
		update(func(s *Workspace) {
			switch a.command {
			case "breakpoint":
				s.toggleCaretBreakpoint()
			case "stop":
				if s.debug.session != nil {
					s.debug.session.Stop()
				}
			default:
				s.debugControl(a.command)
			}
		})
		win.Refresh()
	})

	runCommand := func(id commandID, win *gpui.Window) {
		// Native file dialogs run their own message loop, so they must open
		// after the workspace update has released its entity lease.
		openPicker := PickerMode(0)
		openNativePicker := false
		search := searchModeCount
		var goCommand goAction
		update(func(s *Workspace) {
			s.CommandPalette = nil
			switch id {
			case commandNewFile:
				s.NewFile()
			case commandOpenFile:
				openPicker, openNativePicker = PickerOpenFile, true
			case commandOpenFolder:
				openPicker, openNativePicker = PickerOpenFolder, true
			case commandSave:
				s.Save()
			case commandSaveAll:
				s.SaveAll()
			case commandCloseEditor:
				s.CloseEditor()
			case commandFind:
				s.OpenFind()
			case commandGoToLine:
				s.OpenPrompt("Go to Line", "", "Go", func(ws *Workspace, text string) error {
					if !ws.GoToLine(text) {
						return errInvalidLine{text}
					}
					return nil
				})
			case commandToggleTerminal:
				s.ToggleTerminal(win)
			case commandToggleTree:
				s.ToggleTree()
			case commandToggleMinimap:
				s.Cfg.Editor.ShowMinimap = !s.Cfg.Editor.ShowMinimap
				_ = s.Cfg.Save()
			case commandSettings:
				s.OpenSettings()
			case commandFindFile:
				search = searchFiles
			case commandFindProjectText:
				search = searchText
			case commandFindProjectStructure:
				search = searchStructure
			case commandGoRun:
				goCommand = goRun
			case commandGoBuild:
				goCommand = goBuild
			case commandGoTest:
				goCommand = goTest
			case commandGoVet:
				goCommand = goVet
			case commandGoDebug:
				goCommand = goDebug
			case commandGoDebugTest:
				goCommand = goDebugTest
			case commandDebugBreakpoint:
				s.toggleCaretBreakpoint()
			case commandToggleDebug:
				s.toggleDebug()
			case commandToggleStructure:
				s.selectLeftTool("structure", update, win)
			case commandToggleGit:
				s.selectLeftTool("git", update, win)
			case commandToggleTodos:
				s.toggleTodos(update, win)
			case commandProblems:
				s.toggleProblems()
			}
		})
		if openNativePicker {
			ws.openPicker(openPicker)
			return
		}
		if id == commandEditLaunch {
			ws.editLaunch(update, win)
			return
		}
		if search != searchModeCount {
			ws.openFileSearch(search, update, win)
			return
		}
		if goCommand != "" {
			ws.startGoAction(goCommand, update, win)
			return
		}
		if id == commandFind {
			win.Focus(ws.FocusFind)
		} else if id == commandGoToLine || id == commandSettings {
			win.Focus(ws.FocusModal)
		} else {
			focusEditor(win)
		}
		win.Refresh()
	}

	if ws.Popup != nil {
		popupCfg := ui.PopupConfig{
			State:      ws.Popup,
			Theme:      t,
			WindowSize: winSize,
			Focus:      ws.FocusPopup,
			Measure:    measureUI,
			Update: func(f func(*ui.PopupState)) {
				update(func(s *Workspace) {
					if s.Popup != nil {
						f(s.Popup)
					}
				})
			},
			OnClose:      closePopup,
			RestoreFocus: focusEditor,
		}
		// A top-level dropdown keeps the menu bar itself interactive:
		// the hover strip rides above the dismiss layer so moving the
		// pointer across the bar switches menus (and clicking a title
		// toggles) without closing first — the standard IDE menu-bar
		// behavior.
		if ws.Menu.Open >= 0 {
			popupCfg.MenuBarElement = ui.MenuHoverStrip(menuCfg, ws.TitleBarH, winW)
		}
		root = root.Children(ui.RenderPopupLayer(popupCfg)...)
	}
	if ws.CommandPalette != nil {
		root = root.Children(RenderCommandPalette(ws.CommandPalette, t, ws.FocusCommand,
			func(f func(*CommandPaletteState)) {
				update(func(s *Workspace) {
					if s.CommandPalette != nil {
						f(s.CommandPalette)
					}
				})
			}, runCommand, func(win *gpui.Window) {
				update(func(s *Workspace) { s.CommandPalette = nil })
				focusEditor(win)
				win.Refresh()
			}, winW, winH)...)
	}
	if ws.FileSearch != nil {
		root = root.Children(renderFileSearch(ws, t, update, w, winW, winH)...)
	}
	if ws.Picker != nil {
		root = root.Children(RenderPicker(ws.Picker, t, PickerCallbacks{
			Update: func(f func(*PickerState)) {
				update(func(s *Workspace) {
					if s.Picker != nil {
						f(s.Picker)
					}
				})
			},
			OnOpenFile: func(path string) {
				update(func(s *Workspace) { s.OpenFile(path) })
			},
			OnOpenFolder: func(path string) {
				update(func(s *Workspace) {
					if err := s.OpenFolder(path); err != nil {
						s.SetPromptError(err.Error())
					}
				})
			},
			OnClose:      func() { update(func(s *Workspace) { s.Picker = nil }) },
			RestoreFocus: focusEditor,
			Focus:        ws.FocusModal,
		}, winW, winH)...)
	}
	if ws.launchEditor != nil {
		root = root.Children(renderLaunchEditor(ws, t, update, winSize)...)
	}
	if ws.Prompt != nil {
		root = root.Children(renderPrompt(ws, t, update, focusEditor, winW, winH)...)
	}
	if ws.confirm != nil {
		root = root.Children(renderConfirm(ws, t, update, focusEditor, winW, winH)...)
	}
	if ws.SettingsOpen {
		root = root.Children(renderSettings(ws, t, update, focusEditor, winW, winH)...)
	}
	// The status-bar language selector (above everything except modals).
	if ws.LangMenu != nil {
		root = root.Children(renderLangMenu(ws, t, update, focusEditor, winW, winH)...)
	}

	// Smooth wheel scrolling: while the active buffer's ease trails its
	// target, keep the frame loop alive — the next-frame callback advances
	// the ease one frame at a time (frame-rate independent; it snaps and
	// stops scheduling when it reaches the target). Render runs on the UI
	// thread and the callback fires at draw start, outside any update
	// lease, so the entity update here is the safe pattern (the same one
	// the caret-blink timer uses).
	if ws.scrollAnimating() && !ws.scrollFrameQueued {
		ws.scrollFrameQueued = true
		w.OnNextFrame(func(nw *gpui.Window) {
			ws.scrollFrameQueued = false
			update(func(s *Workspace) { s.advanceScrollAnim() })
			nw.Refresh()
		})
	}
	return root
}

// projectLabel was the menu-bar project name; the label is retired (the
// window title carries the project) and the helper is gone with it.

// wireTerminalRedraw is called before the pane can start its shell. Both
// PTY reader and waiter can fire OnOutput from a worker. Only queue a draw
// via a UI dispatcher; headless callers without one draw snapshots themselves.
func (ws *Workspace) wireTerminalRedraw(draw func()) {
	if ws.Term == nil {
		return
	}
	dispatch := ws.dispatch
	if !ws.ShowTerminal || ws.TermSync || dispatch == nil {
		ws.Term.SetOutputHook(nil)
		return
	}
	// Capture the dispatcher on the UI thread, not the mutable workspace.
	// An async draw runs outside input dispatch, so Refresh alone won't do.
	ws.Term.SetOutputHook(func() { dispatch(draw) })
}

// measureTextWidth measures a string's rendered width through the text
// system (monospace optional).
func measureTextWidth(w *gpui.Window, s string, mono bool) float32 {
	sys := w.TextSystem()
	if sys == nil || s == "" {
		return 0
	}
	f := font.Font{Weight: font.WeightNormal}
	if mono {
		f.Family = "Monospace"
	}
	fid, ok := sys.FontID(f)
	if !ok {
		f.Family = ""
		fid, _ = sys.FontID(f)
	}
	l := sys.LayoutLine(s, geometry.Px(16), []text.FontRun{{Len: len(s), FontID: fid}})
	return l.Width.AsF32()
}

// renderFindBar renders the Ctrl+F bar.
func renderFindBar(ws *Workspace, t ui.Theme, update updateFn, focusEditor func(*gpui.Window), matchLabel string) gpui.Element {
	bar := elements.NewDiv().Flex().ItemsCenter().Gap(ui.DefPx(8)).
		H(ui.Px(ui.FindBarHeight)).FlexShrink(0).Px(ui.DefPx(10)).
		BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1)

	input := ui.RenderInput(ui.InputConfig{
		State:       &ws.FindInput,
		Theme:       t,
		Placeholder: "Find…",
		Focus:       ws.FocusFind,
		Update: func(f func(*ui.InputState)) {
			update(func(s *Workspace) {
				if s.Find != nil {
					f(&s.FindInput)
					if query := s.FindInput.String(); s.Find.Query != query {
						s.Find.Query = query
						s.RefreshFind()
					}
				}
			})
		},
		OnEnter: func(w *gpui.Window) {
			update(func(s *Workspace) { s.FindNext() })
			w.Refresh()
		},
		OnEscape: func(w *gpui.Window) {
			update(func(s *Workspace) { s.CloseFind() })
			focusEditor(w)
			w.Refresh()
		},
	})

	prev := ui.Button("find-prev", "↑", t, false, func(w *gpui.Window) {
		update(func(s *Workspace) { s.FindPrev() })
		w.Refresh()
	})
	next := ui.Button("find-next", "↓", t, false, func(w *gpui.Window) {
		update(func(s *Workspace) { s.FindNext() })
		w.Refresh()
	})
	count := elements.NewText(matchLabel).WithTextColor(t.Dim)
	closeBtn := ui.Button("find-close", "x", t, false, func(w *gpui.Window) {
		update(func(s *Workspace) { s.CloseFind() })
		focusEditor(w)
		w.Refresh()
	})

	return bar.Child(elements.NewDiv().W(ui.Px(240)).Child(input)).
		Child(prev).Child(next).Child(count).
		Child(elements.NewDiv().FlexGrow(1)).Child(closeBtn)
}

// renderPrompt renders a text-input modal.
func renderPrompt(ws *Workspace, t ui.Theme, update updateFn, focusEditor func(*gpui.Window), winW, winH float32) []gpui.Element {
	p := ws.Prompt
	body := func() gpui.Element {
		card := elements.NewDiv().Flex().FlexCol().P(ui.DefPx(14)).Gap(ui.DefPx(10)).
			TrackFocus(ws.FocusModal).
			OnKeyDown(func(ev platform.KeyDownEvent, w *gpui.Window) {
				switch ev.Keystroke.Key {
				case "enter":
					text := p.Value.String()
					update(func(s *Workspace) {
						if s.Prompt == nil {
							return
						}
						if err := s.Prompt.OnSubmit(s, text); err != nil {
							s.Prompt.Err = err.Error()
						} else {
							s.Prompt = nil
						}
					})
					w.Refresh()
				case "escape":
					update(func(s *Workspace) { s.Prompt = nil })
					focusEditor(w)
					w.Refresh()
				}
			})

		input := ui.RenderInput(ui.InputConfig{
			State: &p.Value,
			Theme: t,
			Focus: ws.FocusModal,
			Update: func(f func(*ui.InputState)) {
				update(func(s *Workspace) {
					if s.Prompt != nil {
						f(&s.Prompt.Value)
					}
				})
			},
			OnEnter: func(w *gpui.Window) {
				text := p.Value.String()
				update(func(s *Workspace) {
					if s.Prompt == nil {
						return
					}
					if err := s.Prompt.OnSubmit(s, text); err != nil {
						s.Prompt.Err = err.Error()
					} else {
						s.Prompt = nil
					}
				})
				w.Refresh()
			},
			OnEscape: func(w *gpui.Window) {
				update(func(s *Workspace) { s.Prompt = nil })
				focusEditor(w)
				w.Refresh()
			},
		})
		var errText gpui.Element
		if p.Err != "" {
			errText = ui.MonoText(p.Err, t.Red)
		} else {
			errText = elements.NewDiv().H(ui.Px(1))
		}
		buttons := elements.NewDiv().Flex().JustifyEnd().Gap(ui.DefPx(8)).
			Child(ui.Button("prompt-cancel", "Cancel", t, false, func(w *gpui.Window) {
				update(func(s *Workspace) { s.Prompt = nil })
				focusEditor(w)
				w.Refresh()
			})).
			Child(ui.Button("prompt-submit", p.Submit, t, true, func(w *gpui.Window) {
				text := p.Value.String()
				update(func(s *Workspace) {
					if s.Prompt == nil {
						return
					}
					if err := s.Prompt.OnSubmit(s, text); err != nil {
						s.Prompt.Err = err.Error()
					} else {
						s.Prompt = nil
					}
				})
				w.Refresh()
			}))
		return card.Child(input).Child(errText).Child(buttons)
	}
	return ui.RenderModal(ui.ModalConfig{
		Theme:      t,
		Title:      p.Title,
		Width:      440,
		Height:     190,
		WindowSize: geometry.SizePixels(winW, winH),
		Focus:      ws.FocusModal,
		Body:       body,
		OnClose: func(w *gpui.Window) {
			update(func(s *Workspace) { s.Prompt = nil })
			focusEditor(w)
		},
	})
}

// renderConfirm renders a yes/no modal.
func renderConfirm(ws *Workspace, t ui.Theme, update updateFn, focusEditor func(*gpui.Window), winW, winH float32) []gpui.Element {
	c := ws.confirm
	body := func() gpui.Element {
		return elements.NewDiv().Flex().FlexCol().P(ui.DefPx(14)).Gap(ui.DefPx(12)).
			TrackFocus(ws.FocusModal).
			OnKeyDown(func(ev platform.KeyDownEvent, w *gpui.Window) {
				if ev.Keystroke.Key == "escape" {
					update(func(s *Workspace) { s.confirm = nil })
					focusEditor(w)
					w.Refresh()
				}
			}).
			Child(elements.NewText(c.Text).WithTextColor(t.Text)).
			Child(elements.NewDiv().Flex().JustifyEnd().Gap(ui.DefPx(8)).
				Child(ui.Button("confirm-cancel", "Cancel", t, false, func(w *gpui.Window) {
					update(func(s *Workspace) { s.confirm = nil })
					focusEditor(w)
					w.Refresh()
				})).
				Child(ui.Button("confirm-ok", "Delete", t, true, func(w *gpui.Window) {
					update(func(s *Workspace) {
						if s.confirm == nil {
							return
						}
						if err := s.confirm.OnConfirm(s); err != nil {
							s.SetPromptError(err.Error())
						}
						s.confirm = nil
					})
					focusEditor(w)
					w.Refresh()
				})))
	}
	return ui.RenderModal(ui.ModalConfig{
		Theme:      t,
		Title:      "Confirm",
		Width:      400,
		Height:     160,
		WindowSize: geometry.SizePixels(winW, winH),
		Focus:      ws.FocusModal,
		Body:       body,
		OnClose: func(w *gpui.Window) {
			update(func(s *Workspace) { s.confirm = nil })
			focusEditor(w)
		},
	})
}

// renderEmptyState is the welcome placeholder shown when no buffer is open
// (every tab closed, fresh launch): the app title plus the three entry
// actions - Open File, Open Folder and New File - mirroring what editors
// like VS Code and the upstream editor show on their empty workspace.
func renderEmptyState(ws *Workspace, t ui.Theme, update updateFn, w *gpui.Window) gpui.Element {
	run := func(f func(*Workspace)) func(*gpui.Window) {
		return func(*gpui.Window) {
			update(func(s *Workspace) { f(s) })
			w.Refresh()
		}
	}
	// The native file dialog pumps its own modal message loop, so - exactly
	// like the File menu items - it must run OUTSIDE the entity update
	// lease (openPicker applies its result through the apply hook itself).
	// Calling it inside update() would hold the lease across the dialog's
	// pump and panic when the caret-blink timer updates the entity mid-dialog.
	openFile := func(*gpui.Window) {
		ws.openPicker(PickerOpenFile)
		w.Refresh()
	}
	openFolder := func(*gpui.Window) {
		ws.openPicker(PickerOpenFolder)
		w.Refresh()
	}
	mark := elements.NewDiv().W(ui.Px(52)).H(ui.Px(52)).Flex().ItemsCenter().JustifyCenter().
		Rounded(ui.AbsPx(12)).BgColor(t.Surface1).Border_1().BorderColor(t.Surface2).
		TextSize(ui.AbsPx(20)).Child(ui.MonoText("{}", t.Accent))
	heading, description := "Start working", "Open a project or file to get started"
	if ws.Tree != nil {
		heading, description = "No file open", "Choose a file in Project or open one from disk"
	}
	title := elements.NewDiv().TextSize(ui.AbsPx(22)).
		Child(elements.NewText(heading).WithTextColor(t.Text))
	subtitle := elements.NewDiv().TextSize(ui.AbsPx(13)).Child(elements.NewText(description).WithTextColor(t.Dim))
	hint := elements.NewDiv().TextSize(ui.AbsPx(12)).
		Child(elements.NewText("Ctrl+Shift+P  Command Palette     ·     Ctrl+Space  Completion").WithTextColor(t.Overlay))

	buttons := elements.NewDiv().Flex().ItemsCenter().Gap(ui.DefPx(8)).
		Child(ui.Button("empty-open-file", "Open File…", t, true, openFile)).
		Child(ui.Button("empty-open-folder", "Open Folder…", t, false, openFolder)).
		Child(ui.Button("empty-new-file", "New File", t, false, run(func(s *Workspace) {
			s.NewFile()
		})))

	return elements.NewDiv().
		Flex().FlexCol().FlexGrow(1).FlexShrink(1).
		ItemsCenter().JustifyCenter().
		BgColor(t.EditorBg).
		Child(mark).
		Child(elements.NewDiv().H(ui.Px(18))).
		Child(title).
		Child(elements.NewDiv().H(ui.Px(6))).
		Child(subtitle).
		Child(elements.NewDiv().H(ui.Px(22))).
		Child(buttons).
		Child(elements.NewDiv().H(ui.Px(18))).
		Child(hint)
}

// renderStatusBar reports task and diagnostic status on the left, with
// caret position, language and encoding on the right. Panels open from the rail.
func renderStatusBar(ws *Workspace, t ui.Theme, update updateFn) gpui.Element {
	bar := elements.NewDiv().Flex().ItemsCenter().Gap(ui.DefPx(12)).
		H(ui.Px(ui.StatusHeight)).FlexShrink(0).Px(ui.DefPx(10)).
		TextSize(ui.AbsPx(12)).BgColor(t.Surface).BorderT_1().BorderColor(t.Surface1).
		OverflowHidden()

	if ws.GoTask != nil {
		snap := ws.GoTask.Snapshot()
		bar.Child(elements.NewText(snap.Spec.Label + " · " + snap.Status).WithTextColor(t.Dim))
	}
	problems := ws.problems()
	problemColor := t.Dim
	for _, p := range problems {
		if p.diagnostic.Severity == 1 {
			problemColor = t.Red
			break
		}
		if p.diagnostic.Severity == 2 {
			problemColor = t.Yellow
		}
	}
	bar.Child(elements.NewText(fmt.Sprintf("Problems %d", len(problems))).WithTextColor(problemColor))

	buf := ws.Buf()
	if buf != nil {
		if doc := ws.languageDocs[buf]; doc != nil && doc.status != "" {
			bar = bar.Child(elements.NewText(doc.status).WithTextColor(t.Dim))
		} else if ws.languageStatus != "" {
			bar = bar.Child(elements.NewText(ws.languageStatus).WithTextColor(t.Dim))
		}
		if buf.Dirty {
			bar = bar.Child(elements.NewText("●").WithTextColor(t.Yellow))
		}
		bar = bar.Child(elements.NewDiv().FlexGrow(1))
		// Kind-specific middle/right items.
		switch buf.Kind {
		case editor.KindDiff:
			bar = bar.Child(elements.NewText("Git Diff · Read-only").WithTextColor(t.Dim))
		case editor.KindHex:
			bar = bar.Child(elements.NewText(fmt.Sprintf("%d bytes", len(buf.Data))).WithTextColor(t.Dim))
		case editor.KindImage:
			bar = bar.Child(elements.NewText(editor.ImageInfo(buf)).WithTextColor(t.Dim))
		default:
			selSuffix := ""
			if buf.HasSelection() {
				n := utf8.RuneCountInString(buf.SelectedText())
				selSuffix = fmt.Sprintf(" (%d selected)", n)
			}
			line, col := buf.RuneLineCol()
			bar = bar.Child(elements.NewText(fmt.Sprintf("Ln %d, Col %d%s", line, col, selSuffix)).WithTextColor(t.Dim))
			// Language selector button (VS Code-style): opens the panel
			// above the bar.
			langBtn := elements.NewDiv().
				ID(elements.ButtonID("status-lang-btn")).
				Flex().ItemsCenter().Gap(ui.DefPx(4)).
				Px(ui.DefPx(6)).H(ui.Px(18)).Rounded(ui.AbsPx(4)).
				CursorPointer().
				Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
				OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
					update(func(state *Workspace) {
						if state.LangMenu != nil {
							state.CloseLangMenu()
						} else {
							state.OpenLangMenu()
						}
					})
					w.Focus(ws.FocusLangMenu)
					w.Refresh()
				}).
				Child(elements.NewText(editor.LanguageLabel(buf.LanguageOf())).WithTextColor(t.Green)).
				Child(elements.NewText("▾").WithTextColor(t.Dim))
			bar = bar.Child(langBtn)
		}
	} else {
		bar = bar.Child(elements.NewDiv().FlexGrow(1))
	}
	bar = bar.Child(elements.NewText("UTF-8").WithTextColor(t.Overlay))
	return bar
}

// renderLangMenu renders the status-bar language selector: a scrim, then
// the panel above the status bar's right side — a search field on top and
// a six-row visible window over the FULL catalog, alphabetically sorted:
// the mouse wheel (anywhere over the panel), the arrow keys and
// page-up/down scroll the list; enter applies; escape closes; a slim
// scrollbar on the list's right edge shows how much of the catalog is
// below the fold.
func renderLangMenu(ws *Workspace, t ui.Theme, update updateFn, focusEditor func(*gpui.Window), winW, winH float32) []gpui.Element {
	menu := ws.LangMenu
	if menu == nil {
		return nil
	}
	langs := filteredLanguages(menu.Filter.String())

	// Keep the selection in range as the filter changes.
	sel := menu.Selected
	if sel >= len(langs) {
		sel = len(langs) - 1
	}
	if sel < 0 {
		sel = 0
	}
	// The view clamps to the list bounds ONLY. The selection-follow
	// (scrolling the selected row into view) lives in the key handlers:
	// a render-time clamp here would snap every free wheel scroll back
	// to the selection row, which is exactly the "list will not scroll"
	// bug.
	visible := int(ui.LangMenuVisible)
	maxScroll := len(langs) - visible
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll := menu.Scroll
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}

	input := ui.RenderInput(ui.InputConfig{
		State:       &menu.Filter,
		Theme:       t,
		Placeholder: "Select a language…",
		Focus:       ws.FocusLangMenu,
		Update: func(f func(*ui.InputState)) {
			update(func(s *Workspace) {
				if s.LangMenu != nil {
					// Reset the list cursor ONLY when the
					// filter actually changed: keys the
					// input did not consume (up/down)
					// reach the panel's key handler too,
					// and an unconditional reset would
					// cancel every arrow-key move.
					before := s.LangMenu.Filter.String()
					f(&s.LangMenu.Filter)
					if s.LangMenu.Filter.String() != before {
						s.LangMenu.Selected = 0
						s.LangMenu.Scroll = 0
					}
				}
			})
		},
		OnEscape: func(w *gpui.Window) {
			update(func(s *Workspace) { s.CloseLangMenu() })
			focusEditor(w)
			w.Refresh()
		},
	})

	list := elements.NewDiv().
		ID(elements.ButtonID("lang-menu-list")).
		Flex().FlexCol().
		OverflowHidden().
		My(ui.Px(6)).
		Relative() // the scrollbar rides the list's right edge
	current := ""
	if buf := ws.Buf(); buf != nil {
		current = buf.LanguageOf()
	}
	for i := scroll; i < scroll+visible && i < len(langs); i++ {
		idx := i
		lang := langs[i]
		isCurrent := lang.Id == current
		row := elements.NewDiv().
			ID(elements.ButtonID("lang-" + lang.Id)).
			Flex().ItemsCenter().Gap(ui.DefPx(8)).
			H(ui.Px(ui.LangMenuRowH)).FlexShrink(0).
			Px(ui.DefPx(8)).Rounded(ui.AbsPx(4)).
			CursorPointer().
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).
			OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
				update(func(s *Workspace) { s.SelectLangMenu(idx) })
				focusEditor(w)
				w.Refresh()
			})
		if idx == sel {
			row = row.BgColor(t.Surface1)
		}
		// the upstream editor's picker row: a language badge, the name, and a
		// "(current)" suffix on the buffer's language.
		row = row.Child(langBadge(lang.Id, lang.Name, t)).
			Child(elements.NewText(lang.Name).
				WithTextColor(pick(t.Text, isCurrent, t.Accent)))
		if isCurrent {
			row = row.Child(elements.NewText("(current)").WithTextColor(t.Dim))
		}
		row = row.Child(elements.NewDiv().FlexGrow(1))
		list = list.Child(row)
	}
	if len(langs) == 0 {
		list = list.Child(elements.NewText("No matches").WithTextColor(t.Dim)).
			Child(elements.NewDiv().H(ui.Px(ui.LangMenuRowH)))
	}

	// Slim scrollbar: the fixed six-row window over the full catalog,
	// upstream-style — the thumb shows the position and that there IS more
	// below the fold.
	if len(langs) > visible {
		bodyH := ui.LangMenuRowH * ui.LangMenuVisible
		thumbH := float32(visible) / float32(len(langs)) * float32(bodyH)
		if thumbH < 16 {
			thumbH = 16
		}
		thumbTop := float32(0)
		if maxScroll > 0 {
			thumbTop = float32(scroll) / float32(maxScroll) * (float32(bodyH) - thumbH)
		}
		list = list.Child(elements.NewDiv().
			Absolute().Right(ui.Px(1)).Top(ui.Px(thumbTop)).
			W(ui.Px(4)).H(ui.Px(thumbH)).
			Rounded(ui.AbsPx(2)).
			BgColor(t.Surface2))
	}

	// The list panel: fixed height (6 rows), scrolls internally. The
	// wheel works anywhere over the panel — rows, input or padding.
	wheel := func(ev platform.ScrollEvent, w *gpui.Window) {
		delta := -ev.Delta.PixelDelta(geometry.Px(ui.LangMenuRowH)).Y.AsF32() / ui.LangMenuRowH
		if delta != 0 {
			update(func(s *Workspace) {
				if s.LangMenu != nil {
					s.LangMenu.Scroll += int(delta)
					if s.LangMenu.Scroll < 0 {
						s.LangMenu.Scroll = 0
					}
					if n := len(filteredLanguages(s.LangMenu.Filter.String())) - visible; s.LangMenu.Scroll > n {
						s.LangMenu.Scroll = n
					}
				}
			})
		}
		w.Refresh()
	}
	// follow keeps the selected row inside the view after key moves.
	follow := func(m *LangMenuState, n int) {
		if m.Selected < 0 {
			m.Selected = 0
		}
		if m.Selected > n-1 {
			m.Selected = n - 1
		}
		if m.Selected < m.Scroll {
			m.Scroll = m.Selected
		}
		if m.Selected >= m.Scroll+visible {
			m.Scroll = m.Selected - visible + 1
		}
		if m.Scroll > n-visible {
			m.Scroll = n - visible
		}
		if m.Scroll < 0 {
			m.Scroll = 0
		}
	}
	panel := elements.NewDiv().
		ID(elements.ButtonID("lang-menu")).
		Absolute().Right(ui.Px(12)).Bottom(ui.Px(ui.StatusHeight)).
		W(ui.Px(ui.LangMenuWidth)).
		Flex().FlexCol().
		TrackFocus(ws.FocusLangMenu).
		OnScroll(wheel).
		OnKeyDown(func(ev platform.KeyDownEvent, w *gpui.Window) {
			k := ev.Keystroke
			switch k.Key {
			case "escape":
				update(func(s *Workspace) { s.CloseLangMenu() })
				focusEditor(w)
				w.Refresh()
			case "down":
				update(func(s *Workspace) {
					if s.LangMenu != nil {
						s.LangMenu.Selected++
						follow(s.LangMenu, len(filteredLanguages(s.LangMenu.Filter.String())))
					}
				})
				w.Refresh()
			case "up":
				update(func(s *Workspace) {
					if s.LangMenu != nil {
						s.LangMenu.Selected--
						follow(s.LangMenu, len(filteredLanguages(s.LangMenu.Filter.String())))
					}
				})
				w.Refresh()
			case "pagedown", "pageup":
				step := visible
				if k.Key == "pageup" {
					step = -visible
				}
				update(func(s *Workspace) {
					if s.LangMenu != nil {
						s.LangMenu.Selected += step
						follow(s.LangMenu, len(filteredLanguages(s.LangMenu.Filter.String())))
					}
				})
				w.Refresh()
			case "enter":
				update(func(s *Workspace) { s.SelectLangMenu(sel) })
				focusEditor(w)
				w.Refresh()
			}
		}).
		P(ui.DefPx(8)).
		Rounded(ui.AbsPx(8)).
		BgColor(t.Crust).Border_1().BorderColor(t.Surface1).
		OverflowHidden().
		Child(input).
		Child(list)
	_ = winH
	_ = winW

	// Scrim: click anywhere else closes the menu. It occludes, so the
	// wheel and clicks behind the open menu never reach the editor.
	scrim := elements.NewDiv().
		ID(elements.ButtonID("lang-menu-scrim")).
		Absolute().Left(ui.Px(0)).Top(ui.Px(0)).Right(ui.Px(0)).Bottom(ui.Px(0)).
		Occlude().
		OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
			update(func(s *Workspace) { s.CloseLangMenu() })
			focusEditor(w)
			w.Refresh()
		})
	return []gpui.Element{scrim, panel}
}

// pick returns b when cond, else a.
func pick(a gc.Hsla, cond bool, b gc.Hsla) gc.Hsla {
	if cond {
		return b
	}
	return a
}

// langBadge renders the picker row's language badge: a rounded chip with
// the language's short code in its theme color (the upstream editor shows language icons;
// a text badge needs no icon font and reads at 12px).
func langBadge(id, name string, t ui.Theme) gpui.Element {
	code, color := langBadgeStyle(id, name, t)
	return elements.NewDiv().
		FlexShrink(0).
		Flex().ItemsCenter().JustifyCenter().
		W(ui.Px(30)).H(ui.Px(17)).Rounded(ui.AbsPx(4)).
		BgColor(color.Alpha(0.20)).
		TextSize(ui.AbsPx(10)).
		Child(elements.NewText(code).WithTextColor(color))
}

// langBadgeStyle picks the badge code and color per language.
func langBadgeStyle(id, name string, t ui.Theme) (string, gc.Hsla) {
	switch id {
	case "go":
		return "GO", t.Teal
	case "markdown":
		return "MD", t.Mauve
	case "c":
		return "C", t.Accent
	case "cpp":
		return "C++", t.Accent
	case "csharp":
		return "C#", t.Green
	case "css":
		return "CSS", t.Sky
	case "html":
		return "<>", t.Orange
	case "java":
		return "JV", t.Orange
	case "javascript":
		return "JS", t.Yellow
	case "json":
		return "{ }", t.Peach
	case "php":
		return "PHP", t.Mauve
	case "python":
		return "PY", t.Sky
	case "ruby":
		return "RB", t.Red
	case "rust":
		return "RS", t.Peach
	case "shell":
		return "SH", t.Green
	case "sql":
		return "SQL", t.Teal
	case "toml":
		return "TML", t.Orange
	case "typescript":
		return "TS", t.Accent
	case "xml":
		return "XML", t.Yellow
	case "yaml":
		return "YML", t.Teal
	}
	// Fallback: the first letters of the display name.
	up := strings.ToUpper(name)
	if len([]rune(up)) > 3 {
		up = up[:3]
	}
	return up, t.Dim
}

// renderTitleBar renders the in-app window chrome for client-side
// decorations: the app name on the left, window buttons (minimize /
// maximize-restore / close) on the right. Pressing the bar starts a
// native move gesture; double-clicking toggles maximize. The bar renders
// only when the platform window confirmed custom-decorations support.
func renderTitleBar(ws *Workspace, t ui.Theme, w *gpui.Window) gpui.Element {
	pw := w.PlatformWindow()
	maximized := pw != nil && pw.IsMaximized()

	// A window button: 46x32 hit area, glyph centered, hover highlight;
	// close hovers red (the universal convention).
	winButton := func(name, glyph, tip string, hover gc.Hsla, onClick func(w *gpui.Window)) gpui.Element {
		return elements.NewDiv().
			ID(elements.ButtonID("title-" + name)).
			TooltipBuilder(ui.Tooltip(tip)).
			Flex().ItemsCenter().JustifyCenter().
			W(ui.Px(ui.TitleBarButtonW)).H(ui.Px(ws.TitleBarH - 2)).
			CursorPointer().
			Hover(func(s *style.StyleRefinement) { s.BgColor(hover) }).
			OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
				if ev.Button == platform.ButtonLeft {
					w.StopPropagation() // the bar below must not start a drag
				}
			}).
			OnClick(func(ev gpui.ClickEvent, w *gpui.Window) { onClick(w) }).
			Child(elements.NewText(glyph).WithTextColor(t.Text))
	}

	minBtn := winButton("minimize", "–", "Minimize window", t.Surface2, func(w *gpui.Window) {
		if pw != nil {
			pw.Minimize()
		}
	})
	maxGlyph, maxTip := "□", "Maximize window"
	if maximized {
		maxGlyph, maxTip = "❐", "Restore window"
	}
	maxBtn := winButton("maximize", maxGlyph, maxTip, t.Surface2, func(w *gpui.Window) {
		if pw != nil {
			pw.ToggleMaximize()
		}
	})
	closeBtn := winButton("close", "×", "Close window", t.Red, func(w *gpui.Window) {
		if pw != nil {
			pw.Close()
		}
	})

	bar := elements.NewDiv().
		ID(elements.ButtonID("title-bar")).
		Flex().ItemsCenter().
		H(ui.Px(ws.TitleBarH)).FlexShrink(0).
		Px(ui.DefPx(6)).
		BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1).
		// Pressing the bar runs the native move loop (drag); a click pair
		// (double-click) toggles maximize.
		OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			if ev.Button != platform.ButtonLeft {
				return
			}
			if pw != nil {
				pw.StartWindowDrag()
			}
		}).
		OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
			if ev.Down.ClickCount == 2 && pw != nil {
				pw.ToggleMaximize()
				w.Refresh()
			}
		}).
		Child(elements.NewDiv().W(ui.Px(8))).
		Child(elements.NewDiv().W(ui.Px(20)).H(ui.Px(20)).Rounded(ui.AbsPx(5)).Flex().ItemsCenter().JustifyCenter().BgColor(t.Accent.Alpha(0.22)).
			TextSize(ui.AbsPx(12)).Child(elements.NewText("G").WithTextColor(t.Accent))).
		Child(elements.NewDiv().W(ui.Px(8))).
		Child(elements.NewDiv().TextSize(ui.AbsPx(12)).Child(elements.NewText(ws.Title).WithTextColor(t.Text.Alpha(0.9)))).
		Child(elements.NewDiv().FlexGrow(1)).
		Child(minBtn).
		Child(maxBtn).
		Child(closeBtn)
	return bar
}
