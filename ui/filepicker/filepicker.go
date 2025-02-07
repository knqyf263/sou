// Package filepicker provides a file picker component for terminal user interfaces.
// This implementation is heavily inspired by and borrows from:
// github.com/charmbracelet/bubbles/filepicker/
// The original implementation has been modified to work with fs.FS interface
// instead of the local filesystem.

package filepicker

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dustin/go-humanize"
)

func debug(format string, v ...interface{}) {
	slog.Debug(fmt.Sprintf(format, v...))
}

const (
	marginBottom  = 5
	fileSizeWidth = 7
	paddingLeft   = 2
)

type keyMap struct {
	Up       key.Binding
	Down     key.Binding
	Left     key.Binding
	Right    key.Binding
	Back     key.Binding
	Select   key.Binding
	Quit     key.Binding
	GoToTop  key.Binding
	GoToLast key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	Toggle   key.Binding
	Filter   key.Binding
	Help     key.Binding
	CopyPath key.Binding
}

func defaultKeyMap() keyMap {
	return keyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		Left: key.NewBinding(
			key.WithKeys("left", "h"),
			key.WithHelp("←/h", "back"),
		),
		Right: key.NewBinding(
			key.WithKeys("right", "l"),
			key.WithHelp("→/l", "select"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
		Select: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "select"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		GoToTop: key.NewBinding(
			key.WithKeys("g"),
			key.WithHelp("g", "first"),
		),
		GoToLast: key.NewBinding(
			key.WithKeys("G"),
			key.WithHelp("G", "last"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("K", "pgup"),
			key.WithHelp("pgup", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("J", "pgdown"),
			key.WithHelp("pgdown", "page down"),
		),
		Toggle: key.NewBinding(
			key.WithKeys("."),
			key.WithHelp(".", "toggle hidden"),
		),
		Filter: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "filter"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "toggle help"),
		),
		CopyPath: key.NewBinding(
			key.WithKeys("y", "p"),
			key.WithHelp("yp", "copy path"),
		),
	}
}

type Model struct {
	fs              fs.FS
	keys            keyMap
	selectedIndex   int
	height          int
	currentPath     string
	files           []fs.DirEntry
	styles          Styles
	showHidden      bool
	FileAllowed     bool
	DirAllowed      bool
	selectedFile    string
	selectedAbsPath string
	showPermissions bool
	showSize        bool
	filterStr       string
	filterMode      bool
	showHelp        bool
	lastMessage     string
	messageTimer    int
	pendingKey      string
}

type Styles struct {
	Selected       lipgloss.Style
	Unselected     lipgloss.Style
	Directory      lipgloss.Style
	File           lipgloss.Style
	Error          lipgloss.Style
	Symlink        lipgloss.Style
	Permission     lipgloss.Style
	FileSize       lipgloss.Style
	DisabledFile   lipgloss.Style
	DisabledCursor lipgloss.Style
	EmptyDirectory lipgloss.Style
	Cursor         lipgloss.Style
	Help           lipgloss.Style
}

func DefaultStyles() Styles {
	return Styles{
		Selected:       lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true),
		Unselected:     lipgloss.NewStyle(),
		Directory:      lipgloss.NewStyle().Foreground(lipgloss.Color("99")),
		File:           lipgloss.NewStyle().Foreground(lipgloss.Color("255")),
		Error:          lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
		Symlink:        lipgloss.NewStyle().Foreground(lipgloss.Color("36")),
		Permission:     lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
		FileSize:       lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Width(fileSizeWidth).Align(lipgloss.Right),
		DisabledFile:   lipgloss.NewStyle().Foreground(lipgloss.Color("243")),
		DisabledCursor: lipgloss.NewStyle().Foreground(lipgloss.Color("247")),
		EmptyDirectory: lipgloss.NewStyle().Foreground(lipgloss.Color("240")).PaddingLeft(paddingLeft).SetString("No files found"),
		Cursor:         lipgloss.NewStyle().Foreground(lipgloss.Color("212")),
		Help:           lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
	}
}

func New(fsys fs.FS) Model {
	return Model{
		fs:              fsys,
		keys:            defaultKeyMap(),
		currentPath:     ".",
		styles:          DefaultStyles(),
		FileAllowed:     true,
		DirAllowed:      true,
		showPermissions: true,
		showSize:        true,
		showHelp:        false,
		pendingKey:      "",
	}
}

func (m *Model) Init() tea.Cmd {
	return func() tea.Msg {
		return m.loadFiles("")
	}
}

type errMsg error

type filesLoadedMsg struct {
	files     []fs.DirEntry
	err       error
	focusPath string
}

func (m *Model) loadFiles(focusPath string) tea.Msg {
	debug("===== Loading Files Start =====")
	debug("Loading files for path: %s", m.currentPath)
	debug("Focus path: %s", focusPath)
	debug("Current state:")
	debug("- Selected index: %d", m.selectedIndex)
	debug("- Show hidden: %v", m.showHidden)

	if m.fs == nil {
		return filesLoadedMsg{
			err: fmt.Errorf("filesystem is nil"),
		}
	}

	entries, err := fs.ReadDir(m.fs, m.currentPath)
	if err != nil {
		debug("Error reading directory: %v", err)
		return filesLoadedMsg{
			err: fmt.Errorf("failed to read directory: %w", err),
		}
	}

	var files []fs.DirEntry
	for _, entry := range entries {
		name := entry.Name()
		if !m.showHidden && strings.HasPrefix(name, ".") {
			debug("Skipping hidden file: %s", name)
			continue
		}
		if entry.IsDir() && !m.DirAllowed {
			debug("Skipping directory (not allowed): %s", name)
			continue
		}
		if !entry.IsDir() && !m.FileAllowed {
			debug("Skipping file (not allowed): %s", name)
			continue
		}
		files = append(files, entry)
	}

	sort.Slice(files, func(i, j int) bool {
		// Directories come first
		if files[i].IsDir() && !files[j].IsDir() {
			return true
		}
		if !files[i].IsDir() && files[j].IsDir() {
			return false
		}
		// Then sort by name
		return files[i].Name() < files[j].Name()
	})

	debug("Files loaded and sorted:")
	debug("Total files found: %d", len(files))
	for i, file := range files {
		debug("[%d] %s (isDir: %v)", i, file.Name(), file.IsDir())
	}
	debug("===== Loading Files End =====")

	return filesLoadedMsg{
		files:     files,
		focusPath: focusPath,
	}
}

func (m *Model) getVisibleFiles() []fs.DirEntry {
	if m.filterStr == "" || m.filterStr == "/" {
		return m.files
	}
	filter := strings.ToLower(strings.TrimPrefix(m.filterStr, "/"))
	var filtered []fs.DirEntry
	for _, file := range m.files {
		if strings.Contains(strings.ToLower(file.Name()), filter) {
			filtered = append(filtered, file)
		}
	}
	return filtered
}

func (m Model) getVisibleFilesLength() int {
	return len(m.getVisibleFiles())
}

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle filter mode keys first
		if m.filterMode {
			switch msg.Type {
			case tea.KeyEsc:
				m.filterStr = ""
				m.filterMode = false
				return m, nil
			case tea.KeyBackspace:
				if len(m.filterStr) > 1 { // Keep the initial "/"
					m.filterStr = m.filterStr[:len(m.filterStr)-1]
					// Adjust cursor position if it's out of bounds
					visibleLen := m.getVisibleFilesLength()
					if visibleLen > 0 && m.selectedIndex >= visibleLen {
						m.selectedIndex = visibleLen - 1
					}
				}
				return m, nil
			case tea.KeyEnter:
				m.filterMode = false
				return m, nil
			case tea.KeyRunes:
				m.filterStr += msg.String()
				// Adjust cursor position if it's out of bounds
				visibleLen := m.getVisibleFilesLength()
				if visibleLen > 0 && m.selectedIndex >= visibleLen {
					m.selectedIndex = visibleLen - 1
				}
				return m, nil
			default:
				return m, nil // Ignore all other keys in filter mode
			}
		}

		// Handle key sequences
		if m.pendingKey != "" {
			switch m.pendingKey {
			case "y":
				switch msg.String() {
				case "p":
					// Handle yp command
					visibleFiles := m.getVisibleFiles()
					if len(visibleFiles) == 0 {
						m.pendingKey = ""
						return m, nil
					}
					selected := visibleFiles[m.selectedIndex]
					path := filepath.Join(m.currentPath, selected.Name())
					if err := copyToClipboard(path); err != nil {
						m.lastMessage = fmt.Sprintf("❌ Failed to copy path: %v", err)
					} else {
						m.lastMessage = "📋 Path copied to clipboard"
					}
					m.messageTimer = 30
					m.pendingKey = ""
					return m, tick()
				default:
					m.pendingKey = ""
					return m, nil
				}
			default:
				m.pendingKey = ""
				return m, nil
			}
		}

		// Handle initial key press
		switch msg.String() {
		case "y":
			m.pendingKey = "y"
			return m, nil
		}

		// Handle normal mode keys
		switch {
		case key.Matches(msg, m.keys.Help):
			m.showHelp = !m.showHelp
			return m, nil
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Up):
			if m.selectedIndex > 0 {
				m.selectedIndex--
			}
		case key.Matches(msg, m.keys.Down):
			visibleLen := m.getVisibleFilesLength()
			if m.selectedIndex < visibleLen-1 {
				m.selectedIndex++
			}
		case key.Matches(msg, m.keys.GoToTop):
			m.selectedIndex = 0
		case key.Matches(msg, m.keys.GoToLast):
			visibleLen := m.getVisibleFilesLength()
			m.selectedIndex = visibleLen - 1
		case key.Matches(msg, m.keys.PageUp):
			m.selectedIndex -= m.height
			if m.selectedIndex < 0 {
				m.selectedIndex = 0
			}
		case key.Matches(msg, m.keys.PageDown):
			visibleLen := m.getVisibleFilesLength()
			m.selectedIndex += m.height
			if m.selectedIndex >= visibleLen {
				m.selectedIndex = visibleLen - 1
			}
		case key.Matches(msg, m.keys.Left), key.Matches(msg, m.keys.Back):
			if m.currentPath != "." {
				// Get the current directory name before going up
				currentBase := filepath.Base(m.currentPath)
				parentPath := filepath.Dir(m.currentPath)

				// Normalize paths
				if strings.HasPrefix(parentPath, "./") {
					parentPath = strings.TrimPrefix(parentPath, "./")
				}
				if parentPath == "/" || parentPath == "." || parentPath == "" {
					parentPath = "."
				}

				m.currentPath = parentPath
				m.selectedIndex = 0
				m.selectedFile = ""
				m.selectedAbsPath = ""

				return m, func() tea.Msg {
					return m.loadFiles(currentBase)
				}
			}
		case key.Matches(msg, m.keys.Right), key.Matches(msg, m.keys.Select):
			visibleFiles := m.getVisibleFiles()
			if len(visibleFiles) == 0 {
				return m, nil
			}
			selected := visibleFiles[m.selectedIndex]
			if selected.IsDir() {
				newPath := filepath.Join(m.currentPath, selected.Name())
				m.currentPath = newPath
				m.selectedIndex = 0
				m.selectedFile = ""
				m.selectedAbsPath = ""

				return m, func() tea.Msg {
					return m.loadFiles("")
				}
			} else if m.FileAllowed {
				m.selectedFile = selected.Name()
				m.selectedAbsPath = filepath.Join(m.currentPath, selected.Name())
				return m, nil
			}
		case key.Matches(msg, m.keys.Toggle):
			m.showHidden = !m.showHidden
			return m, func() tea.Msg {
				return m.loadFiles("")
			}
		case key.Matches(msg, m.keys.Filter):
			if !m.filterMode {
				m.filterStr = "/"
				m.filterMode = true
				return m, nil
			}
		}
		return m, nil

	case filesLoadedMsg:
		if msg.err != nil {
			debug("Error in filesLoadedMsg: %v", msg.err)
			return m, nil
		}

		m.files = msg.files

		debug("===== Files Loaded Message Processing Start =====")
		debug("Current state:")
		debug("- Current path: %s", m.currentPath)
		debug("- Number of files: %d", len(m.files))
		debug("- Current selected index: %d", m.selectedIndex)
		debug("- Focus path: %s", msg.focusPath)

		// If focusPath is specified, try to find and focus on that directory
		if msg.focusPath != "" {
			for i, file := range m.files {
				if file.Name() == msg.focusPath {
					m.selectedIndex = i
					debug("Found focus path at index: %d", i)
					break
				}
			}
		}

		// Ensure selected index is within bounds
		if m.selectedIndex >= len(m.files) {
			m.selectedIndex = len(m.files) - 1
			debug("- Adjusted to last item: %d", m.selectedIndex)
		}
		if m.selectedIndex < 0 {
			m.selectedIndex = 0
			debug("- Adjusted to first item: %d", m.selectedIndex)
		}

		debug("Final state:")
		debug("- Selected index: %d", m.selectedIndex)
		if m.selectedIndex < len(m.files) {
			debug("- Selected file: %s", m.files[m.selectedIndex].Name())
		}
		debug("===== Files Loaded Message Processing End =====")

		return m, nil

	case errMsg:
		return m, nil

	case tickMsg:
		if m.messageTimer > 0 {
			m.messageTimer--
			if m.messageTimer == 0 {
				m.lastMessage = ""
				return m, nil
			}
			return m, tick()
		}
	}

	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	visibleFiles := m.getVisibleFiles()
	var s strings.Builder

	// Show current path and filter
	s.WriteString(m.styles.Directory.Render(fmt.Sprintf("Directory: %s", m.currentPath)))
	if m.filterStr != "" {
		s.WriteString("\n")
		s.WriteString(m.styles.File.Render(fmt.Sprintf("Filter: %s", m.filterStr)))
	}
	s.WriteString("\n\n")

	if len(visibleFiles) == 0 {
		s.WriteString(m.styles.EmptyDirectory.String())
		// Add padding for help text
		s.WriteString(strings.Repeat("\n", m.height-6))
		if m.lastMessage != "" {
			s.WriteString("\n")
			s.WriteString(m.styles.File.Render(m.lastMessage))
		}
		return s.String()
	}

	// Show files
	for i, file := range visibleFiles {
		if i >= m.selectedIndex-m.height+marginBottom && i <= m.selectedIndex+m.height-marginBottom {
			s.WriteString(m.renderFile(file, i))
			s.WriteString("\n")
		}
	}

	// Add a few blank lines after the file list
	s.WriteString("\n")

	// Show message if exists
	if m.lastMessage != "" {
		s.WriteString(m.styles.File.Render(m.lastMessage))
		s.WriteString("\n")
	}

	// Add remaining padding for help text
	remainingLines := m.height - len(visibleFiles) - 6 // 6 for header, margins, and extra blank lines
	if remainingLines > 0 {
		s.WriteString(strings.Repeat("\n", remainingLines))
	}

	return s.String()
}

func (m Model) renderFile(file fs.DirEntry, index int) string {
	info, err := file.Info()
	if err != nil {
		return ""
	}

	name := file.Name()
	style := m.styles.Unselected
	cursor := " "

	if index == m.selectedIndex {
		style = m.styles.Selected
		cursor = ">"
	}

	// Build the line
	var line strings.Builder

	// Add cursor
	line.WriteString(cursor + " ")

	// Add permissions if enabled
	if m.showPermissions {
		line.WriteString(m.styles.Permission.Render(info.Mode().String()) + " ")
	}

	// Add size if enabled
	if m.showSize {
		size := humanize.Bytes(uint64(info.Size()))
		line.WriteString(m.styles.FileSize.Render(size) + " ")
	}

	// Add name with appropriate style
	if file.IsDir() {
		name += "/"
		if index == m.selectedIndex {
			style = style.Inherit(m.styles.Directory)
		} else {
			style = m.styles.Directory
		}
	} else {
		if index == m.selectedIndex {
			style = style.Inherit(m.styles.File)
		} else {
			style = m.styles.File
		}
	}

	line.WriteString(style.Render(name))

	// Add symlink indicator if it's a symlink
	if info.Mode()&fs.ModeSymlink != 0 {
		line.WriteString(" → " + m.styles.Symlink.Render("(symlink)"))
	}

	return line.String()
}

func (m *Model) SetHeight(height int) {
	m.height = height
}

func (m *Model) SelectedFile() (name string, absPath string, ok bool) {
	visibleFiles := m.getVisibleFiles()
	if len(visibleFiles) == 0 || m.selectedIndex >= len(visibleFiles) {
		return "", "", false
	}
	selected := visibleFiles[m.selectedIndex]
	if selected.IsDir() {
		return "", "", false
	}
	name = selected.Name()
	absPath = filepath.Join(m.currentPath, name)
	return name, absPath, true
}

func (m *Model) CurrentPath() string {
	return m.currentPath
}

func (m *Model) SetShowHidden(show bool) {
	m.showHidden = show
}

func (m *Model) ShowHidden() bool {
	return m.showHidden
}

func (m *Model) SetShowPermissions(show bool) {
	m.showPermissions = show
}

func (m *Model) SetShowSize(show bool) {
	m.showSize = show
}

func (m *Model) SetPath(path string) {
	m.currentPath = path
	m.selectedIndex = 0
	m.selectedFile = ""
	m.selectedAbsPath = ""
}

func (m Model) InFilterMode() bool {
	return m.filterMode
}

func copyToClipboard(text string) error {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	case "linux":
		cmd := exec.Command("xclip", "-selection", "clipboard")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	case "windows":
		cmd := exec.Command("clip")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	default:
		return fmt.Errorf("unsupported platform")
	}
}
