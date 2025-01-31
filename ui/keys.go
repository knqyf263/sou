package ui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	quit         key.Binding
	enter        key.Binding
	back         key.Binding
	toggleHidden key.Binding
	export       key.Binding
	nextTab      key.Binding
	prevTab      key.Binding
	copyDiffID   key.Binding
	copyPath     key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		enter: key.NewBinding(
			key.WithKeys("enter", "l", "right"),
			key.WithHelp("enter/l/→", "view/open"),
		),
		back: key.NewBinding(
			key.WithKeys("h", "backspace", "esc", "left"),
			key.WithHelp("h/esc/←", "back"),
		),
		toggleHidden: key.NewBinding(
			key.WithKeys("."),
			key.WithHelp(".", "toggle hidden"),
		),
		export: key.NewBinding(
			key.WithKeys("x"),
			key.WithHelp("x", "export file to current directory"),
		),
		nextTab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next tab"),
		),
		prevTab: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "previous tab"),
		),
		copyDiffID: key.NewBinding(
			key.WithKeys("y y"),
			key.WithHelp("yy", "copy diff ID"),
		),
		copyPath: key.NewBinding(
			key.WithKeys("y", "p"),
			key.WithHelp("yp", "copy path"),
		),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.enter, k.back, k.toggleHidden, k.export, k.nextTab, k.prevTab, k.copyDiffID, k.copyPath, k.quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.enter, k.back, k.toggleHidden},
		{k.export, k.nextTab, k.prevTab, k.copyDiffID, k.copyPath, k.quit},
	}
}
