package filepicker

import (
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockFS is a simple in-memory filesystem for testing
type mockFS struct {
	fstest.MapFS
}

func newMockFS() *mockFS {
	return &mockFS{
		MapFS: fstest.MapFS{},
	}
}

func (m *mockFS) addFile(name string, content []byte, mode fs.FileMode) {
	m.MapFS[name] = &fstest.MapFile{
		Data:    content,
		Mode:    mode,
		ModTime: time.Now(),
	}
}

func (m *mockFS) addDir(name string) {
	m.MapFS[name] = &fstest.MapFile{
		Mode:    fs.ModeDir,
		ModTime: time.Now(),
	}
}

func setupTestFS() *mockFS {
	fs := newMockFS()
	// Add test directories in sorted order
	fs.addDir("testdir")
	fs.addDir("testdir/subdir")
	fs.addDir(".hidden_dir")

	// Add test files in sorted order (to ensure consistent test results)
	fs.addFile("file1.txt", []byte("content1"), 0o644)
	fs.addFile("file2.txt", []byte("content2"), 0o644)
	fs.addFile("file3.txt", []byte("content3"), 0o644)
	fs.addFile("testdir/file4.txt", []byte("content4"), 0o644)
	fs.addFile("testdir/file5.txt", []byte("content5"), 0o644)
	fs.addFile("testdir/subdir/file6.txt", []byte("content6"), 0o644)
	fs.addFile(".hidden_file", []byte("hidden"), 0o644)
	fs.addFile("testdir/.hidden_file2", []byte("hidden2"), 0o644)

	return fs
}

func TestNewModel(t *testing.T) {
	fs := setupTestFS()
	m := New(fs)

	assert.Equal(t, ".", m.currentPath)
	assert.True(t, m.FileAllowed)
	assert.True(t, m.DirAllowed)
	assert.True(t, m.showPermissions)
	assert.True(t, m.showSize)
	assert.False(t, m.showHelp)
	assert.Equal(t, "", m.pendingKey)
}

func TestModelInitialFileLoad(t *testing.T) {
	fs := setupTestFS()
	m := New(fs)
	cmd := m.Init()
	msg := cmd()

	loadedMsg, ok := msg.(filesLoadedMsg)
	require.True(t, ok)
	require.NoError(t, loadedMsg.err)
	// 3 visible files + 1 visible dir (excluding hidden)
	assert.Len(t, loadedMsg.files, 4)
}

func TestModelNavigation(t *testing.T) {
	fs := setupTestFS()
	m := New(fs)
	cmd := m.Init()
	msg := cmd()
	loadedMsg := msg.(filesLoadedMsg)
	m.files = loadedMsg.files

	tests := []struct {
		name           string
		keyMsg         tea.KeyMsg
		expectedIndex  int
		expectedPath   string
		expectedLength int
	}{
		{
			name:           "move down",
			keyMsg:         tea.KeyMsg{Type: tea.KeyDown},
			expectedIndex:  1,
			expectedPath:   ".",
			expectedLength: 4, // 3 files + 1 dir
		},
		{
			name:           "move up",
			keyMsg:         tea.KeyMsg{Type: tea.KeyUp},
			expectedIndex:  0,
			expectedPath:   ".",
			expectedLength: 4,
		},
		{
			name:           "enter directory",
			keyMsg:         tea.KeyMsg{Type: tea.KeyRight},
			expectedIndex:  0,
			expectedPath:   "testdir",
			expectedLength: 3, // 2 visible files + 1 subdir
		},
		{
			name:           "go back",
			keyMsg:         tea.KeyMsg{Type: tea.KeyLeft},
			expectedIndex:  0,
			expectedPath:   ".",
			expectedLength: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newModel, cmd := m.Update(tt.keyMsg)
			model := newModel

			if cmd != nil {
				msg := cmd()
				if loadedMsg, ok := msg.(filesLoadedMsg); ok {
					model.files = loadedMsg.files
				}
			}

			assert.Equal(t, tt.expectedIndex, model.selectedIndex)
			assert.Equal(t, tt.expectedPath, model.currentPath)
			assert.Equal(t, tt.expectedLength, len(model.getVisibleFiles()))
		})
	}
}

func TestModelFilter(t *testing.T) {
	fs := setupTestFS()
	m := New(fs)
	cmd := m.Init()
	msg := cmd()
	loadedMsg := msg.(filesLoadedMsg)
	m.files = loadedMsg.files

	tests := []struct {
		name          string
		filterStr     string
		expectedFiles int
		expectedIndex int
	}{
		{
			name:          "no filter",
			filterStr:     "",
			expectedFiles: 4, // 3 files + 1 dir
			expectedIndex: 0,
		},
		{
			name:          "filter by 'file'",
			filterStr:     "/file",
			expectedFiles: 3, // file1.txt, file2.txt, file3.txt
			expectedIndex: 0,
		},
		{
			name:          "filter by '1'",
			filterStr:     "/1",
			expectedFiles: 1,
			expectedIndex: 0,
		},
		{
			name:          "filter with no matches",
			filterStr:     "/xyz",
			expectedFiles: 0,
			expectedIndex: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.filterStr = tt.filterStr
			visibleFiles := m.getVisibleFiles()
			assert.Equal(t, tt.expectedFiles, len(visibleFiles))
			assert.Equal(t, tt.expectedIndex, m.selectedIndex)
		})
	}
}

func TestToggleHidden(t *testing.T) {
	fs := setupTestFS()
	m := New(fs)

	// Initial state (hidden files not shown)
	cmd := m.Init()
	msg := cmd()
	loadedMsg := msg.(filesLoadedMsg)
	require.NoError(t, loadedMsg.err)
	m.files = loadedMsg.files

	visibleFiles := m.getVisibleFiles()
	assert.Equal(t, 4, len(visibleFiles), "Expected 4 visible files (3 files + 1 dir)")

	// Toggle hidden files on
	m.showHidden = true
	cmd = m.Init() // Reload files with hidden files shown
	msg = cmd()
	loadedMsg, ok := msg.(filesLoadedMsg)
	require.True(t, ok)
	require.NoError(t, loadedMsg.err)
	m.files = loadedMsg.files

	visibleFiles = m.getVisibleFiles()
	assert.Equal(t, 6, len(visibleFiles), "Expected 6 files (3 files + 2 dirs + 1 hidden file) in root")
}

func TestFileSelection(t *testing.T) {
	fs := setupTestFS()
	m := New(fs)
	cmd := m.Init()
	msg := cmd()
	loadedMsg := msg.(filesLoadedMsg)
	m.files = loadedMsg.files

	// Verify initial file list
	visibleFiles := m.getVisibleFiles()
	require.GreaterOrEqual(t, len(visibleFiles), 1, "At least one file should be visible")

	// Find first regular file
	fileIndex := -1
	for i, f := range visibleFiles {
		if !f.IsDir() {
			fileIndex = i
			break
		}
	}
	require.NotEqual(t, -1, fileIndex, "Regular file should be found in visible files")

	// Select first file
	m.selectedIndex = fileIndex
	name, absPath, ok := m.SelectedFile()
	assert.True(t, ok)
	assert.Equal(t, "file1.txt", name)
	assert.Equal(t, "file1.txt", absPath)

	// Find directory
	dirIndex := -1
	for i, f := range visibleFiles {
		if f.IsDir() {
			dirIndex = i
			break
		}
	}
	require.NotEqual(t, -1, dirIndex, "Directory should be found in visible files")

	m.selectedIndex = dirIndex
	name, absPath, ok = m.SelectedFile()
	assert.False(t, ok)
	assert.Equal(t, "", name)
	assert.Equal(t, "", absPath)
}

func TestKeyboardShortcuts(t *testing.T) {
	fs := setupTestFS()
	m := New(fs)
	cmd := m.Init()
	msg := cmd()
	loadedMsg := msg.(filesLoadedMsg)
	m.files = loadedMsg.files

	tests := []struct {
		name           string
		keyMsg         tea.KeyMsg
		expectedIndex  int
		expectedAction string
	}{
		{
			name:           "go to top with 'g'",
			keyMsg:         tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}},
			expectedIndex:  0,
			expectedAction: "top",
		},
		{
			name:           "go to bottom with 'G'",
			keyMsg:         tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}},
			expectedIndex:  3, // Last visible item
			expectedAction: "bottom",
		},
		{
			name:           "toggle help with '?'",
			keyMsg:         tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}},
			expectedIndex:  0,
			expectedAction: "help",
		},
		{
			name:           "quit with 'q'",
			keyMsg:         tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}},
			expectedIndex:  0,
			expectedAction: "quit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newModel, cmd := m.Update(tt.keyMsg)
			m = newModel

			switch tt.expectedAction {
			case "top":
				assert.Equal(t, 0, m.selectedIndex)
			case "bottom":
				assert.Equal(t, len(m.getVisibleFiles())-1, m.selectedIndex)
			case "help":
				assert.True(t, m.showHelp)
			case "quit":
				assert.NotNil(t, cmd)
			}
		})
	}
}

func TestPagination(t *testing.T) {
	fs := setupTestFS()
	m := New(fs)
	cmd := m.Init()
	msg := cmd()
	loadedMsg := msg.(filesLoadedMsg)
	m.files = loadedMsg.files
	m.height = 2 // Set small height to test pagination

	tests := []struct {
		name          string
		keyMsg        tea.KeyMsg
		initialIndex  int
		expectedIndex int
	}{
		{
			name:          "page down from top",
			keyMsg:        tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'J'}},
			initialIndex:  0,
			expectedIndex: 2,
		},
		{
			name:          "page up from bottom",
			keyMsg:        tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'K'}},
			initialIndex:  3,
			expectedIndex: 1,
		},
		{
			name:          "page down at bottom",
			keyMsg:        tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'J'}},
			initialIndex:  3,
			expectedIndex: 3,
		},
		{
			name:          "page up at top",
			keyMsg:        tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'K'}},
			initialIndex:  0,
			expectedIndex: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.selectedIndex = tt.initialIndex
			newModel, _ := m.Update(tt.keyMsg)
			m = newModel
			assert.Equal(t, tt.expectedIndex, m.selectedIndex)
		})
	}
}

func TestPathOperations(t *testing.T) {
	fs := setupTestFS()
	m := New(fs)

	tests := []struct {
		name          string
		path          string
		expectedPath  string
		expectedError bool
	}{
		{
			name:          "set valid path",
			path:          "testdir",
			expectedPath:  "testdir",
			expectedError: false,
		},
		{
			name:          "set root path",
			path:          ".",
			expectedPath:  ".",
			expectedError: false,
		},
		{
			name:          "set nested path",
			path:          "testdir/subdir",
			expectedPath:  "testdir/subdir",
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.SetPath(tt.path)
			assert.Equal(t, tt.expectedPath, m.CurrentPath())

			cmd := m.Init()
			msg := cmd()
			loadedMsg, ok := msg.(filesLoadedMsg)
			require.True(t, ok)

			if tt.expectedError {
				assert.Error(t, loadedMsg.err)
			} else {
				assert.NoError(t, loadedMsg.err)
			}
		})
	}
}

func TestErrorCases(t *testing.T) {
	// Test with nil filesystem
	m := New(nil)
	cmd := m.Init()
	msg := cmd()
	loadedMsg, ok := msg.(filesLoadedMsg)
	require.True(t, ok)
	assert.Error(t, loadedMsg.err)
	assert.Contains(t, loadedMsg.err.Error(), "filesystem is nil")

	// Create a mock filesystem that returns errors
	errorFS := &mockFS{
		MapFS: fstest.MapFS{
			"error_dir": &fstest.MapFile{
				Mode: fs.ModeDir | 0o000, // No permissions
			},
		},
	}

	m = New(errorFS)
	m.SetPath("nonexistent")
	cmd = m.Init()
	msg = cmd()
	loadedMsg, ok = msg.(filesLoadedMsg)
	require.True(t, ok)
	assert.Error(t, loadedMsg.err)
	assert.Contains(t, loadedMsg.err.Error(), "failed to read directory")
}

func TestFilterMode(t *testing.T) {
	fs := setupTestFS()
	m := New(fs)
	cmd := m.Init()
	msg := cmd()
	loadedMsg := msg.(filesLoadedMsg)
	m.files = loadedMsg.files

	// Enter filter mode
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = newModel
	assert.True(t, m.InFilterMode())
	assert.Equal(t, "/", m.filterStr)

	// Type in filter mode
	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = newModel
	assert.True(t, m.InFilterMode())
	assert.Equal(t, "/f", m.filterStr)

	// Exit filter mode with Escape
	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = newModel
	assert.False(t, m.InFilterMode())
	assert.Equal(t, "", m.filterStr)
}
