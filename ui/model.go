package ui

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/daemon"
	"github.com/knqyf263/sou/container"
	"github.com/knqyf263/sou/ui/filepicker"
)

func debug(format string, v ...interface{}) {
	slog.Debug(fmt.Sprintf(format, v...))
}

type Mode int

const (
	LayerMode Mode = iota
	FileMode
	ViewMode
	LoadingMode
	ManifestMode
	ConfigMode
	PullingMode
	padding  = 2
	maxWidth = 100
)

type errMsg struct {
	err error
}

type imageLoadedMsg struct {
	image        *container.Image
	isLocalImage bool
}

type progressMsg float64

type layerItem struct {
	diffID  string
	size    int64
	command string
}

func (i layerItem) Title() string {
	return i.command
}

func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

func (i layerItem) Description() string {
	return fmt.Sprintf("DiffID: %s  Size: %s", i.diffID, formatSize(i.size))
}

func (i layerItem) FilterValue() string {
	return i.command + " " + i.diffID
}

type fileItem struct {
	file container.File
}

func (i fileItem) Title() string {
	if i.file.IsDir {
		return i.file.Name + "/"
	}
	return i.file.Name
}

func (i fileItem) Description() string {
	return fmt.Sprintf("%s  %s  %s", i.file.Mode, formatSize(i.file.Size), i.file.ModTime)
}

func (i fileItem) FilterValue() string {
	return i.file.Name
}

type Model struct {
	list           list.Model
	viewport       viewport.Model
	filepicker     filepicker.Model
	keys           keyMap
	mode           Mode
	ready          bool
	width          int
	height         int
	image          *container.Image
	currentLayer   *container.Layer
	pendingLayer   *container.Layer
	currentPath    string
	currentFile    *container.File
	message        string
	tabs           []string
	activeTab      int
	tabStyle       lipgloss.Style
	activeTabStyle lipgloss.Style
	progress       float64
	loadingBar     progress.Model
	spinner        spinner.Model
	isLocalImage   bool
	showHelp       bool
	pendingKey     string
}

type loadingLayerMsg struct {
	layer *container.Layer
	err   error
}

type viewFileMsg struct {
	content string
	err     error
}

type exportFileMsg struct {
	err error
}

type hideMessageMsg struct{}

type containerFS struct {
	layer *container.Layer
}

func (c *containerFS) Open(filePath string) (fs.File, error) {
	if c.layer == nil {
		return nil, fmt.Errorf("layer is nil")
	}

	// Convert path for tarfs
	tarfsPath := filePath
	if filePath == "." {
		tarfsPath = "/"
	} else if filePath != "" && filePath[0] == '/' {
		tarfsPath = filePath[1:]
	}

	return &containerDir{layer: c.layer, path: tarfsPath}, nil
}

type containerDir struct {
	layer *container.Layer
	path  string
	files []container.File
	pos   int
}

func (d *containerDir) Read([]byte) (int, error) {
	return 0, fmt.Errorf("cannot read from directory")
}

func (d *containerDir) Close() error {
	return nil
}

func (d *containerDir) Stat() (fs.FileInfo, error) {
	return containerFileInfo{isDir: true}, nil
}

func (d *containerDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if d.files == nil {
		// Convert path for tarfs
		tarfsPath := d.path
		if tarfsPath == "/" {
			tarfsPath = "."
		} else if tarfsPath != "" && tarfsPath[0] == '/' {
			tarfsPath = tarfsPath[1:]
		}

		files, err := d.layer.GetFiles(tarfsPath)
		if err != nil {
			return nil, err
		}
		d.files = files
	}

	if d.pos >= len(d.files) {
		if n <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}

	var entries []fs.DirEntry
	for i := 0; i < n && d.pos < len(d.files); i++ {
		entries = append(entries, containerDirEntry{file: d.files[d.pos]})
		d.pos++
	}

	if n <= 0 {
		for d.pos < len(d.files) {
			entries = append(entries, containerDirEntry{file: d.files[d.pos]})
			d.pos++
		}
	}

	return entries, nil
}

type containerDirEntry struct {
	file container.File
}

func (e containerDirEntry) Name() string {
	return e.file.Name
}

func (e containerDirEntry) IsDir() bool {
	return e.file.IsDir
}

func (e containerDirEntry) Type() fs.FileMode {
	return fs.FileMode(0)
}

func (e containerDirEntry) Info() (fs.FileInfo, error) {
	return containerFileInfo{
		name:    e.file.Name,
		size:    e.file.Size,
		isDir:   e.file.IsDir,
		modTime: time.Now(),
	}, nil
}

type containerFileInfo struct {
	name    string
	size    int64
	isDir   bool
	modTime time.Time
}

func (i containerFileInfo) Name() string {
	return i.name
}

func (i containerFileInfo) Size() int64 {
	return i.size
}

func (i containerFileInfo) Mode() fs.FileMode {
	return fs.FileMode(0o644)
}

func (i containerFileInfo) ModTime() time.Time {
	return i.modTime
}

func (i containerFileInfo) IsDir() bool {
	return i.isDir
}

func (i containerFileInfo) Sys() interface{} {
	return nil
}

// Global channel for progress updates
var progressChan chan float64

type copyToClipboardMsg struct {
	err error
}

// Add this function to get the appropriate clipboard command
func getClipboardCmd() (cmd string, args []string) {
	switch runtime.GOOS {
	case "darwin":
		return "pbcopy", nil
	case "linux":
		return "xclip", []string{"-selection", "clipboard"}
	default:
		return "", nil
	}
}

func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		debug("Attempting to copy text to clipboard: %s", text)

		cmd, args := getClipboardCmd()
		if cmd == "" {
			err := fmt.Errorf("clipboard command not supported on this OS")
			debug("Clipboard error: %v", err)
			return copyToClipboardMsg{err: err}
		}

		debug("Using clipboard command: %s with args: %v", cmd, args)
		clipCmd := exec.Command(cmd, args...)
		clipCmd.Stdin = strings.NewReader(text)

		if err := clipCmd.Run(); err != nil {
			debug("Failed to copy to clipboard: %v", err)
			return copyToClipboardMsg{err: fmt.Errorf("failed to copy to clipboard: %w", err)}
		}

		debug("Successfully copied to clipboard")
		return copyToClipboardMsg{err: nil}
	}
}

// Custom colors for the application
var (
	selectedColor  = lipgloss.Color("#61AFEF") // A calm blue for selected items
	normalColor    = lipgloss.Color("#ABB2BF") // A soft white for normal items
	dimmedColor    = lipgloss.Color("#636D83") // A muted color for less important text
	highlightColor = lipgloss.Color("#FFB86C") // A soft orange for highlights (filter, etc)
)

// newCustomList creates a new list with custom styling
func newCustomList(items []list.Item, width, height int) list.Model {
	delegate := list.NewDefaultDelegate()

	// Custom styles for the delegate
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(selectedColor).
		Background(lipgloss.NoColor{}).
		BorderLeft(true).
		BorderLeftForeground(selectedColor).
		Bold(true)

	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(lipgloss.Color("#4B5669")). // Darker and more muted color for selected description
		Background(lipgloss.NoColor{}).
		BorderLeft(true).
		BorderLeftForeground(selectedColor)

	delegate.Styles.NormalTitle = delegate.Styles.NormalTitle.
		Foreground(normalColor).
		BorderLeft(true).
		BorderLeftForeground(lipgloss.NoColor{})

	delegate.Styles.NormalDesc = delegate.Styles.NormalDesc.
		Foreground(lipgloss.Color("#3E4551")). // Even darker color for normal description
		BorderLeft(true).
		BorderLeftForeground(lipgloss.NoColor{})

	// Create the list
	l := list.New(items, delegate, width, height)
	l.SetShowTitle(false)
	l.SetFilteringEnabled(true)
	l.DisableQuitKeybindings()
	l.SetShowHelp(false)

	// Custom styles for the list
	l.Styles.Title = l.Styles.Title.
		Foreground(selectedColor).
		Bold(true).
		Padding(0, 1)

	l.Styles.FilterPrompt = l.Styles.FilterPrompt.
		Foreground(highlightColor)

	l.Styles.FilterCursor = l.Styles.FilterCursor.
		Foreground(highlightColor)

	l.Styles.NoItems = l.Styles.NoItems.
		Foreground(dimmedColor)

	return l
}

func NewModel(ref string) (Model, tea.Cmd) {
	// Check if image exists locally first
	reference, err := name.ParseReference(ref)
	if err != nil {
		return Model{}, func() tea.Msg {
			return errMsg{fmt.Errorf("failed to parse reference: %w", err)}
		}
	}

	isLocalImage := false
	if _, err := daemon.Image(reference); err == nil {
		debug("Found local image during initial check")
		isLocalImage = true
	} else {
		debug("Image not found locally during initial check")
	}

	// Create a new channel for progress updates
	progressChan = make(chan float64, 100)

	// Create an initial empty list with custom styling
	l := newCustomList([]list.Item{}, 0, 0)
	l.Title = "Loading..."

	// Initialize loading bar
	loadingBar := progress.New(
		progress.WithDefaultGradient(),
		progress.WithoutPercentage(),
	)

	// Initialize spinner
	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(selectedColor)

	debug("Creating new model with isLocalImage=%v", isLocalImage)
	m := Model{
		list:           l,
		tabs:           []string{"📦 Layers", "📄 Manifest", "⚙️  Config"},
		activeTab:      0,
		tabStyle:       lipgloss.NewStyle().Padding(0, 2).Foreground(dimmedColor),
		activeTabStyle: lipgloss.NewStyle().Padding(0, 2).Foreground(selectedColor).Bold(true),
		mode:           PullingMode,
		keys:           newKeyMap(),
		currentPath:    "/",
		filepicker:     filepicker.New(&containerFS{}),
		loadingBar:     loadingBar,
		spinner:        s,
		isLocalImage:   isLocalImage,
	}

	// Create a command that will load the image
	loadCmd := func() tea.Msg {
		image, isLocal, err := container.NewImage(ref, func(progress float64) {
			debug("Progress callback: %.2f", progress)
			select {
			case progressChan <- progress:
				debug("Progress sent to channel: %.2f", progress)
			default:
				debug("Progress channel full: %.2f", progress)
			}
		})
		if err != nil {
			close(progressChan)
			return errMsg{err}
		}
		close(progressChan)
		debug("Image loaded, returning imageLoadedMsg with isLocalImage=%v", isLocal)
		return imageLoadedMsg{image: image, isLocalImage: isLocal}
	}

	return m, tea.Batch(tickCmd(), loadCmd, s.Tick)
}

func (m *Model) Init() tea.Cmd {
	return m.filepicker.Init()
}

type manifestMsg struct {
	content string
	err     error
}

type configMsg struct {
	content string
	err     error
}

// Add tickMsg type
type tickMsg time.Time

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if !m.ready {
			m.ready = true
		}

		contentWidth := msg.Width - 4
		if m.mode == LoadingMode {
			m.loadingBar.Width = contentWidth
		}

		if m.mode == ViewMode || m.mode == ManifestMode || m.mode == ConfigMode {
			m.viewport.Width = contentWidth
			m.viewport.Height = msg.Height - 6
		} else if m.mode == FileMode {
			m.filepicker.SetHeight(m.height - 6)
		} else {
			m.list.SetSize(contentWidth, msg.Height-6)
		}

		return m, nil

	case spinner.TickMsg:
		if m.mode == PullingMode {
			var cmd tea.Cmd
			newModel := m
			newModel.spinner, cmd = m.spinner.Update(msg)
			return newModel, cmd
		}
		return m, nil

	case errMsg:
		m.message = fmt.Sprintf("Error: %v", msg.err)
		m.mode = LayerMode
		return m, hideMessageAfter(3 * time.Second)

	case tickMsg:
		var cmds []tea.Cmd

		// Always queue up the next tick
		cmds = append(cmds, tickCmd())

		// Check for progress updates if channel exists
		if progressChan != nil {
			select {
			case progressUpdate, ok := <-progressChan:
				if ok {
					debug("Progress received in tick: %.2f", progressUpdate)
					newModel := m
					newModel.progress = progressUpdate
					return newModel, tea.Batch(cmds...)
				}
			default:
			}
		}

		// Update progress bars
		if m.mode == LoadingMode {
			if m.loadingBar.Percent() == 1.0 {
				// If we have a pending layer and progress is complete, trigger transition
				if m.pendingLayer != nil {
					return m, tea.Tick(time.Millisecond*200, func(t time.Time) tea.Msg {
						return transitionMsg{}
					})
				}
				return m, nil
			}
			newModel := m
			cmd := newModel.loadingBar.SetPercent(m.progress)
			cmds = append(cmds, cmd)
			return newModel, tea.Batch(cmds...)
		}
		return m, tea.Batch(cmds...)

	case progressMsg:
		debug("Progress message received: %.2f", float64(msg))
		newModel := m
		newModel.progress = float64(msg)
		return newModel, nil

	case imageLoadedMsg:
		debug("Image loaded message received: isLocalImage=%v", msg.isLocalImage)
		newModel := m
		newModel.image = msg.image
		newModel.isLocalImage = msg.isLocalImage
		newModel.mode = LayerMode
		debug("Model updated: isLocalImage=%v, mode=%v", newModel.isLocalImage, newModel.mode)

		var items []list.Item
		for _, layer := range msg.image.Layers {
			items = append(items, layerItem{
				diffID:  layer.DiffID,
				size:    layer.Size,
				command: layer.Command,
			})
		}

		l := newCustomList(items, m.width-4, m.height-6)
		newModel.list = l
		debug("Returning new model: isLocalImage=%v, mode=%v", newModel.isLocalImage, newModel.mode)
		return newModel, nil

	case tea.KeyMsg:
		// Handle quit key (Ctrl-C) in any mode
		if key.Matches(msg, m.keys.quit) {
			return m, tea.Quit
		}

		// Skip other key handling during loading or pulling
		if m.mode == LoadingMode || m.mode == PullingMode {
			return m, nil
		}

		// Handle help toggle
		if msg.String() == "?" {
			newModel := m
			newModel.showHelp = !m.showHelp
			return newModel, nil
		}

		// Handle 'y' key in LayerMode
		if m.mode == LayerMode && msg.String() == "y" {
			if m.pendingKey == "y" {
				// Second 'y' press - copy diff ID
				if item, ok := m.list.SelectedItem().(layerItem); ok {
					m.pendingKey = ""
					m.message = "📋 Diff ID copied to clipboard"
					return m, tea.Batch(
						copyToClipboard(item.diffID),
						hideMessageAfter(3*time.Second),
					)
				}
			} else {
				// First 'y' press
				m.pendingKey = "y"
			}
			return m, nil
		}
		// Reset pending key if any other key is pressed
		if m.pendingKey != "" {
			m.pendingKey = ""
		}

		// Check if in filter mode
		if m.mode == LayerMode && m.list.FilterState() == list.Filtering {
			m.list, cmd = m.list.Update(msg)
			return m, cmd
		}
		if m.mode == FileMode && m.filepicker.InFilterMode() {
			m.filepicker, cmd = m.filepicker.Update(msg)
			return m, cmd
		}

		switch {
		case key.Matches(msg, m.keys.nextTab):
			if m.mode != ViewMode {
				m.activeTab = (m.activeTab + 1) % len(m.tabs)
				switch m.activeTab {
				case 0: // Layers
					if m.mode == FileMode {
						// Keep the current file mode state
						return m, nil
					}
					m.mode = LayerMode
				case 1: // Manifest
					m.mode = ManifestMode
					return m, func() tea.Msg {
						content, err := m.image.GetManifestWithColor(false)
						if err != nil {
							return manifestMsg{err: err}
						}
						return manifestMsg{content: string(colorizeJSON(content))}
					}
				case 2: // Config
					m.mode = ConfigMode
					return m, func() tea.Msg {
						content, err := m.image.GetConfigWithColor(false)
						if err != nil {
							return configMsg{err: err}
						}
						return configMsg{content: string(colorizeJSON(content))}
					}
				}
			}
			return m, nil
		case key.Matches(msg, m.keys.prevTab):
			if m.mode != ViewMode {
				m.activeTab = (m.activeTab - 1 + len(m.tabs)) % len(m.tabs)
				switch m.activeTab {
				case 0: // Layers
					if m.mode == FileMode {
						// Keep the current file mode state
						return m, nil
					}
					m.mode = LayerMode
				case 1: // Manifest
					m.mode = ManifestMode
					return m, func() tea.Msg {
						content, err := m.image.GetManifestWithColor(false)
						if err != nil {
							return manifestMsg{err: err}
						}
						return manifestMsg{content: string(colorizeJSON(content))}
					}
				case 2: // Config
					m.mode = ConfigMode
					return m, func() tea.Msg {
						content, err := m.image.GetConfigWithColor(false)
						if err != nil {
							return configMsg{err: err}
						}
						return configMsg{content: string(colorizeJSON(content))}
					}
				}
			}
			return m, nil
		case key.Matches(msg, m.keys.toggleHidden) && m.mode == FileMode:
			m.filepicker.SetShowHidden(!m.filepicker.ShowHidden())
			return m, nil
		case key.Matches(msg, m.keys.export):
			switch m.mode {
			case FileMode:
				files, err := m.currentLayer.GetFiles(m.filepicker.CurrentPath())
				if err != nil {
					m.message = fmt.Sprintf("Failed to get files: %v", err)
					return m, hideMessageAfter(3 * time.Second)
				}

				if fileName, _, ok := m.filepicker.SelectedFile(); ok {
					for _, file := range files {
						if file.Name == fileName {
							if !file.IsDir {
								return m, tea.Batch(
									exportFile(m.currentLayer, file),
									hideMessageAfter(3*time.Second),
								)
							}
						}
					}
				}
			case ManifestMode:
				return m, tea.Batch(
					exportManifest(m.image),
					hideMessageAfter(3*time.Second),
				)
			case ConfigMode:
				return m, tea.Batch(
					exportConfig(m.image),
					hideMessageAfter(3*time.Second),
				)
			}
		case key.Matches(msg, m.keys.enter):
			if m.mode == LayerMode {
				if item, ok := m.list.SelectedItem().(layerItem); ok {
					for i := range m.image.Layers {
						if m.image.Layers[i].DiffID == item.diffID {
							layerCopy := m.image.Layers[i]
							m.mode = LoadingMode
							m.progress = 0.0
							m.loadingBar = progress.New(
								progress.WithDefaultGradient(),
								progress.WithoutPercentage(),
							)
							progressWidth := m.width - padding*2 - 4
							if progressWidth > maxWidth {
								progressWidth = maxWidth
							}
							m.loadingBar.Width = progressWidth
							return m, initializeLayer(&layerCopy)
						}
					}
				}
			} else if m.mode == FileMode {
				files, err := m.currentLayer.GetFiles(m.filepicker.CurrentPath())
				if err != nil {
					m.message = fmt.Sprintf("Failed to get files: %v", err)
					return m, hideMessageAfter(3 * time.Second)
				}

				if fileName, _, ok := m.filepicker.SelectedFile(); ok {
					for _, file := range files {
						if file.Name == fileName {
							if file.IsDir {
								m.currentPath = file.Path
								newPath := filepath.Join(m.filepicker.CurrentPath(), fileName)
								m.filepicker.SetPath(newPath)
								return m, m.filepicker.Init()
							} else {
								m.currentFile = &file
								m.mode = LoadingMode
								return m, viewFile(m.currentLayer, file.Path)
							}
						}
					}
				}
			}
		case key.Matches(msg, m.keys.back):
			if m.mode == FileMode {
				// If filepicker is in filter mode, let it handle the key
				if m.filepicker.InFilterMode() {
					m.filepicker, cmd = m.filepicker.Update(msg)
					return m, cmd
				}
				// Check if we're at root and 'h' key was pressed
				if m.filepicker.CurrentPath() == "." && msg.String() == "h" {
					// If we're at the root of the filepicker and 'h' was pressed, go back to layer mode
					m.mode = LayerMode
					m.currentLayer = nil
					m.currentPath = "/"
					var items []list.Item
					for _, layer := range m.image.Layers {
						items = append(items, layerItem{
							diffID:  layer.DiffID,
							size:    layer.Size,
							command: layer.Command,
						})
					}
					m.list.SetItems(items)
					m.updateTitle()
					m.list.Select(0)
					return m, nil
				}
				// Let filepicker handle back navigation
				m.filepicker, cmd = m.filepicker.Update(msg)
				return m, cmd
			} else if m.mode == ViewMode {
				m.mode = FileMode
				m.updateTitle()
				return m, nil
			} else if m.mode == ManifestMode || m.mode == ConfigMode {
				if m.currentLayer != nil {
					// If we came from file mode, go back to file mode
					m.mode = FileMode
					m.activeTab = 0
					m.updateTitle()
					return m, nil
				}
				// Otherwise go back to layer mode
				m.mode = LayerMode
				m.activeTab = 0
				m.updateTitle()
				return m, nil
			}
		}

	case manifestMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("Failed to get manifest: %v", msg.err)
			return m, hideMessageAfter(3 * time.Second)
		}
		m.viewport = viewport.New(m.width-4, m.height-6)
		m.viewport.SetContent(msg.content)
		return m, nil

	case configMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("Failed to get config: %v", msg.err)
			return m, hideMessageAfter(3 * time.Second)
		}
		m.viewport = viewport.New(m.width-4, m.height-6)
		m.viewport.SetContent(msg.content)
		return m, nil

	case loadingLayerMsg:
		if msg.err != nil {
			m.mode = LayerMode
			m.message = fmt.Sprintf("Failed to load layer: %v", msg.err)
			m.updateTitle()
			return m, hideMessageAfter(3 * time.Second)
		}

		debug("Received loadingLayerMsg, layer: %v, progress: %.2f", msg.layer != nil, m.progress)

		// Set progress to 100% before transitioning
		if m.mode == LoadingMode {
			// First update to 100%
			m.progress = 1.0
			progressModel, cmd := m.loadingBar.Update(msg)
			m.loadingBar = progressModel.(progress.Model)
			m.loadingBar.SetPercent(1.0)

			// Store the layer for transition
			m.pendingLayer = msg.layer

			// Wait for the progress bar to reach 100% before transitioning
			if m.loadingBar.Percent() < 1.0 {
				return m, cmd
			}

			// Wait a bit to show 100% progress, then transition
			return m, tea.Sequence(
				cmd,
				tea.Tick(time.Millisecond*200, func(t time.Time) tea.Msg {
					return transitionMsg{}
				}),
			)
		}

		return m, nil

	case viewFileMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("Failed to read file: %v", msg.err)
			return m, hideMessageAfter(3 * time.Second)
		}
		m.viewport = viewport.New(m.width-4, m.height-6)
		m.viewport.SetContent(msg.content)
		m.mode = ViewMode
		return m, nil

	case exportFileMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("Failed to export file: %v", msg.err)
		} else {
			m.message = "File exported successfully"
		}
		return m, hideMessageAfter(3 * time.Second)

	case hideMessageMsg:
		m.message = ""
		return m, nil

	case transitionMsg:
		m.currentLayer = m.pendingLayer
		m.mode = FileMode
		m.currentPath = "/"
		m.filepicker = filepicker.New(&containerFS{layer: m.pendingLayer})
		m.filepicker.SetHeight(m.height - 6)
		m.filepicker.SetShowHidden(true)
		return m, m.filepicker.Init()

	case progress.FrameMsg:
		if m.mode == LoadingMode {
			progressModel, cmd := m.loadingBar.Update(msg)
			m.loadingBar = progressModel.(progress.Model)
			return m, cmd
		}
		return m, nil

	case copyToClipboardMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("Error: %v", msg.err)
			debug("Clipboard error message displayed: %v", msg.err)
		} else {
			if item, ok := m.list.SelectedItem().(layerItem); ok {
				m.message = fmt.Sprintf("Copied diff ID to clipboard: %s", item.diffID)
				debug("Copied diff ID to clipboard: %s", item.diffID)
			}
		}
		return m, hideMessageAfter(3 * time.Second)
	}

	switch m.mode {
	case ViewMode, ManifestMode, ConfigMode:
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	case FileMode:
		var pickerCmd tea.Cmd
		m.filepicker, pickerCmd = m.filepicker.Update(msg)
		cmds = append(cmds, pickerCmd)
	default:
		m.list, cmd = m.list.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) View() string {
	if !m.ready {
		return "\n  Loading..."
	}

	var view string
	switch m.mode {
	case LayerMode:
		baseView := m.list.View()

		// Split the view into content and padding
		parts := strings.Split(baseView, "\n")

		// Find where the actual content ends (before padding)
		contentEnd := 0
		for i := len(parts) - 1; i >= 0; i-- {
			if strings.TrimSpace(parts[i]) != "" {
				contentEnd = i + 1
				break
			}
		}

		// Reconstruct the view with message and help
		var finalView strings.Builder
		finalView.WriteString(strings.Join(parts[:contentEnd], "\n"))

		// Add message if exists
		if m.message != "" {
			finalView.WriteString("\n\n  💡 ")
			finalView.WriteString(m.message)
			finalView.WriteString("\n")
		}

		// Calculate space needed for help text
		helpHeight := 1 // Simple help
		if m.showHelp {
			helpHeight = 14 // Detailed help
		}

		// Calculate remaining space
		usedLines := contentEnd
		if m.message != "" {
			usedLines += 3 // 2 for spacing + 1 for message
		}
		remainingLines := m.height - usedLines - helpHeight - 5 // Subtract 5 for bottom padding (including help text)

		// Add remaining space
		if remainingLines > 0 {
			finalView.WriteString(strings.Repeat("\n", remainingLines))
		}

		// Add help text
		helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		if m.showHelp {
			finalView.WriteString("\n" +
				"Navigation:\n" +
				"  ↑/k: up\n" +
				"  ↓/j: down\n" +
				"  →/l: view layer\n" +
				"  g: first\n" +
				"  G: last\n" +
				"  K/pgup: page up\n" +
				"  J/pgdown: page down\n" +
				"\nActions:\n" +
				"  yy: copy diff ID\n" +
				"  /: filter layers\n" +
				"  ?: toggle help\n" +
				"  q: quit\n\n\n\n\n")
		} else {
			finalView.WriteString("\n" + helpStyle.Render("↑/k up • ↓/j down • →/l view layer • / filter • q quit • ? more") + "\n\n\n\n\n")
		}

		view = finalView.String()
	case ViewMode:
		view = m.viewport.View()
	case LoadingMode:
		progressWidth := m.width - padding*2 - 4
		if progressWidth > maxWidth {
			progressWidth = maxWidth
		}
		m.loadingBar.Width = progressWidth
		view = fmt.Sprintf("\n\n  ⏳ Loading layer...\n%s", lipgloss.NewStyle().PaddingLeft(padding).Render(m.loadingBar.View()))
	case PullingMode:
		if m.isLocalImage {
			debug("View: Showing local image message with spinner")
			view = fmt.Sprintf("\n\n  %s Loading local image...", m.spinner.View())
		} else {
			debug("View: Showing remote image message with spinner")
			view = fmt.Sprintf("\n\n  %s Pulling image from registry...", m.spinner.View())
		}
	case FileMode:
		baseView := m.filepicker.View()

		// Define help style
		helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

		// Split the view into content and padding
		parts := strings.Split(baseView, "\n")

		// Find where the actual content ends (before padding)
		contentEnd := 0
		for i := len(parts) - 1; i >= 0; i-- {
			if parts[i] != "" {
				contentEnd = i + 1
				break
			}
		}

		// Reconstruct the view with message and help in correct positions
		var finalView strings.Builder

		// Add content (including the original padding)
		finalView.WriteString(strings.Join(parts[:contentEnd], "\n"))

		// Add message if exists
		if m.message != "" {
			finalView.WriteString("\n\n  💡 ")
			finalView.WriteString(m.message)
			finalView.WriteString("\n")
		}

		// Calculate space needed for help text
		helpHeight := 1 // Simple help
		if m.showHelp {
			helpHeight = 16 // Detailed help: 14 lines for content + 1 for initial newline + 1 for extra newline before Actions
		}

		// Calculate remaining space
		usedLines := contentEnd
		if m.message != "" {
			usedLines += 3 // 2 for spacing + 1 for message
		}
		remainingLines := m.height - usedLines - helpHeight - 4 // Subtract 4 for bottom padding
		if remainingLines > 0 {
			finalView.WriteString(strings.Repeat("\n", remainingLines))
		}

		// Add help text
		if m.showHelp {
			finalView.WriteString("Navigation:\n" +
				"  ↑/k: up\n" +
				"  ↓/j: down\n" +
				"  ←/h: back\n" +
				"  →/l: view/open\n" +
				"  g: first\n" +
				"  G: last\n" +
				"  K/pgup: page up\n" +
				"  J/pgdown: page down\n" +
				"  tab: next tab\n" +
				"  shift+tab: previous tab\n" +
				"\nActions:\n" +
				"  .: toggle hidden\n" +
				"  x: export file\n" +
				"  /: filter files\n" +
				"  ?: toggle help\n" +
				"  q: quit\n\n\n\n") // Add 4 newlines after help text
		} else {
			finalView.WriteString(helpStyle.Render("↑/k up • ↓/j down • →/l view/open • ←/h back • tab switch • / filter • q quit • ? more") + "\n\n\n\n") // Add 4 newlines after help text
		}

		view = finalView.String()
	case ManifestMode, ConfigMode:
		baseView := m.viewport.View()

		// Split the view into content and padding
		parts := strings.Split(baseView, "\n")

		// Find where the actual content ends (before padding)
		contentEnd := 0
		for i := len(parts) - 1; i >= 0; i-- {
			if strings.TrimSpace(parts[i]) != "" {
				contentEnd = i + 1
				break
			}
		}

		// Reconstruct the view with message and help
		var finalView strings.Builder

		// Add content
		finalView.WriteString(strings.Join(parts[:contentEnd], "\n"))

		// Calculate space needed for help text
		helpHeight := 2 // Simple help (1 for help text + 1 for initial newline)
		if m.showHelp {
			helpHeight = 14 // Detailed help: 12 lines for content + 1 for initial newline + 1 for extra newline before Actions
		}

		// Calculate remaining space
		usedLines := contentEnd
		if m.message != "" {
			usedLines += 3 // 2 for spacing + 1 for message
		}
		remainingLines := m.height - usedLines - helpHeight - 4 // Subtract 4 for bottom padding

		// Add message if exists
		if m.message != "" {
			finalView.WriteString("\n\n  💡 ")
			finalView.WriteString(m.message)
			finalView.WriteString("\n") // Add newline after message
		}

		// Add remaining space
		if remainingLines > 0 {
			finalView.WriteString(strings.Repeat("\n", remainingLines))
		}

		// Add help text
		helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		if m.showHelp {
			finalView.WriteString("\n" +
				"Navigation:\n" +
				"  ↑/k: up\n" +
				"  ↓/j: down\n" +
				"  ←/h: back\n" +
				"  g: first\n" +
				"  G: last\n" +
				"  K/pgup: page up\n" +
				"  J/pgdown: page down\n" +
				"\nActions:\n" +
				"  x: export JSON\n" +
				"  ?: toggle help\n" +
				"  q: quit\n\n\n\n") // Add 4 newlines after help text
		} else {
			finalView.WriteString("\n" + helpStyle.Render("↑/k up • ↓/j down • x export • q quit • ? more") + "\n\n\n\n") // Add 4 newlines after help text
		}

		view = finalView.String()
	default:
		view = m.list.View()
	}

	// Render tabs
	var tabViews []string
	for i, tab := range m.tabs {
		style := m.tabStyle
		if i == m.activeTab {
			style = m.activeTabStyle
		}
		tabViews = append(tabViews, style.Render(tab))
	}
	tabs := lipgloss.JoinHorizontal(lipgloss.Top, tabViews...)
	tabs = lipgloss.NewStyle().BorderBottom(true).Render(tabs)

	view = strings.TrimRight(view, "\n")
	return fmt.Sprintf("%s\n%s", tabs, view)
}

func (m *Model) updateTitle() {
	switch m.mode {
	case LayerMode:
		m.list.SetShowFilter(true)
	}
}

func (m *Model) showFiles(layer *container.Layer, path string) error {
	if layer == nil {
		return fmt.Errorf("layer is nil")
	}

	// Convert root path "/" to "." for tarfs
	tarfsPath := path
	if path == "/" {
		tarfsPath = "."
	} else if path != "" && path[0] == '/' {
		tarfsPath = path[1:]
	}

	files, err := layer.GetFiles(tarfsPath)
	if err != nil {
		return fmt.Errorf("failed to get files: %w", err)
	}

	var items []list.Item
	for _, file := range files {
		items = append(items, fileItem{file: file})
	}

	m.list.SetItems(items)
	m.currentLayer = layer
	m.currentPath = path
	// Update the title with current location
	m.updateTitle()
	// Always select the first item after setting items
	m.list.Select(0)
	return nil
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*50, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func initializeLayer(layer *container.Layer) tea.Cmd {
	// Create a new channel for progress updates
	progressChan = make(chan float64, 100)

	debug("Starting layer initialization")

	// Create a command that will initialize the layer
	loadCmd := func() tea.Msg {
		if layer == nil {
			debug("Layer is nil, returning error")
			close(progressChan)
			return loadingLayerMsg{layer: nil, err: fmt.Errorf("invalid layer")}
		}

		debug("Starting layer initialization process")
		err := layer.InitializeLayer(func(progress float64) {
			select {
			case progressChan <- progress:
				debug("Progress sent to channel: %.2f", progress)
			default:
				debug("Progress channel full: %.2f", progress)
			}
		})

		debug("Layer initialization completed with error: %v", err)
		close(progressChan)

		if err != nil {
			return loadingLayerMsg{layer: nil, err: fmt.Errorf("failed to initialize layer: %w", err)}
		}

		return loadingLayerMsg{layer: layer}
	}

	return tea.Batch(tickCmd(), loadCmd)
}

func viewFile(layer *container.Layer, path string) tea.Cmd {
	return func() tea.Msg {
		if layer == nil {
			return viewFileMsg{err: fmt.Errorf("layer is nil")}
		}

		// Convert path for tarfs
		tarfsPath := path
		if path != "" && path[0] == '/' {
			tarfsPath = path[1:]
		}

		content, err := layer.ReadFile(tarfsPath)
		if err != nil {
			return viewFileMsg{err: fmt.Errorf("failed to read file: %w", err)}
		}

		return viewFileMsg{content: string(content)}
	}
}

func exportFile(layer *container.Layer, file container.File) tea.Cmd {
	return func() tea.Msg {
		if layer == nil {
			return exportFileMsg{err: fmt.Errorf("layer is nil")}
		}

		// Convert path for tarfs
		tarfsPath := file.Path
		if len(tarfsPath) > 0 && tarfsPath[0] == '/' {
			tarfsPath = tarfsPath[1:]
		}

		content, err := layer.ReadFile(tarfsPath)
		if err != nil {
			return exportFileMsg{err: fmt.Errorf("failed to read file: %w", err)}
		}

		// Get current working directory
		cwd, err := os.Getwd()
		if err != nil {
			return exportFileMsg{err: fmt.Errorf("failed to get current directory: %w", err)}
		}

		// Create output file in current directory
		outputPath := filepath.Join(cwd, file.Name)
		if err := os.WriteFile(outputPath, content, 0644); err != nil {
			return exportFileMsg{err: fmt.Errorf("failed to write file: %w", err)}
		}

		return exportFileMsg{err: nil}
	}
}

func hideMessageAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return hideMessageMsg{}
	})
}

func exportFileToPath(layer *container.Layer, file container.File, outputPath string) error {
	if layer == nil {
		return fmt.Errorf("layer is nil")
	}

	// Convert path for tarfs
	tarfsPath := file.Path
	if len(tarfsPath) > 0 && tarfsPath[0] == '/' {
		tarfsPath = tarfsPath[1:]
	}

	content, err := layer.ReadFile(tarfsPath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// If outputPath is a directory, append the filename
	fi, err := os.Stat(outputPath)
	if err == nil && fi.IsDir() {
		outputPath = filepath.Join(outputPath, file.Name)
	}

	if err := os.WriteFile(outputPath, content, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// Add a new message type for transition
type transitionMsg struct{}

// Add new export functions
func exportManifest(image *container.Image) tea.Cmd {
	return func() tea.Msg {
		if image == nil {
			return exportFileMsg{err: fmt.Errorf("image is nil")}
		}

		content, err := image.GetManifestWithColor(false)
		if err != nil {
			return exportFileMsg{err: fmt.Errorf("failed to get manifest: %w", err)}
		}

		// Get current working directory
		cwd, err := os.Getwd()
		if err != nil {
			return exportFileMsg{err: fmt.Errorf("failed to get current directory: %w", err)}
		}

		// Create output file in current directory
		outputPath := filepath.Join(cwd, "manifest.json")
		if err := os.WriteFile(outputPath, content, 0644); err != nil {
			return exportFileMsg{err: fmt.Errorf("failed to write file: %w", err)}
		}

		return exportFileMsg{err: nil}
	}
}

func exportConfig(image *container.Image) tea.Cmd {
	return func() tea.Msg {
		if image == nil {
			return exportFileMsg{err: fmt.Errorf("image is nil")}
		}

		content, err := image.GetConfigWithColor(false)
		if err != nil {
			return exportFileMsg{err: fmt.Errorf("failed to get config: %w", err)}
		}

		// Get current working directory
		cwd, err := os.Getwd()
		if err != nil {
			return exportFileMsg{err: fmt.Errorf("failed to get current directory: %w", err)}
		}

		// Create output file in current directory
		outputPath := filepath.Join(cwd, "config.json")
		if err := os.WriteFile(outputPath, content, 0644); err != nil {
			return exportFileMsg{err: fmt.Errorf("failed to write file: %w", err)}
		}

		return exportFileMsg{err: nil}
	}
}

// colorizeJSON adds ANSI color codes to JSON string
func colorizeJSON(input []byte) []byte {
	var out strings.Builder
	content := string(input)
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		// Find the position of the first non-whitespace character
		firstChar := len(line) - len(strings.TrimLeft(line, " "))

		// Extract key and value
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)

		if len(parts) == 2 {
			// Line contains both key and value
			keyStr := strings.Trim(parts[0], `" ,`)
			value := strings.TrimSpace(parts[1])

			// Add colors
			coloredKey := fmt.Sprintf("\x1b[36m%s\x1b[0m", keyStr) // Cyan for keys
			coloredValue := value

			// Color different types of values
			switch {
			case strings.HasPrefix(value, `"`):
				// String values in green
				coloredValue = fmt.Sprintf("\x1b[32m%s\x1b[0m", value)
			case strings.HasPrefix(value, "{") || strings.HasPrefix(value, "["):
				// Objects and arrays in yellow
				coloredValue = fmt.Sprintf("\x1b[33m%s\x1b[0m", value)
			case value == "true" || value == "false":
				// Booleans in magenta
				coloredValue = fmt.Sprintf("\x1b[35m%s\x1b[0m", value)
			case strings.ContainsAny(value, "0123456789"):
				// Numbers in blue
				coloredValue = fmt.Sprintf("\x1b[34m%s\x1b[0m", value)
			}

			// Reconstruct the line with proper indentation
			out.WriteString(strings.Repeat(" ", firstChar))
			out.WriteString(`"`)
			out.WriteString(coloredKey)
			out.WriteString(`": `)
			out.WriteString(coloredValue)
			out.WriteString("\n")
		} else {
			// Line contains only structural elements (braces, brackets, etc.)
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				// Structural elements in yellow
				out.WriteString(strings.Repeat(" ", firstChar))
				out.WriteString(fmt.Sprintf("\x1b[33m%s\x1b[0m", trimmed))
				out.WriteString("\n")
			} else {
				out.WriteString("\n")
			}
		}
	}

	return []byte(out.String())
}
