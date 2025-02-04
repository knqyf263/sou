package tarfs_test

import (
	"archive/tar"
	"bytes"
	"io"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/knqyf263/sou/tarfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestTar creates a temporary tar file from the given fs.FS
func createTestTar(t *testing.T) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	defer tw.Close()

	files := []struct {
		name     string
		content  string
		isDir    bool
		modTime  time.Time
		mode     fs.FileMode
		uid      int
		gid      int
		size     int64
		typeflag byte
	}{
		{
			name:    "dir1",
			isDir:   true,
			mode:    fs.ModeDir | 0755,
			modTime: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "dir1/dir2",
			isDir:   true,
			mode:    fs.ModeDir | 0755,
			modTime: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "file1.txt",
			content: "Hello, World!",
			mode:    0644,
			modTime: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "dir1/file2.txt",
			content: "Hello from dir1!",
			mode:    0644,
			modTime: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "dir1/dir2/file3.txt",
			content: "Hello from dir2!",
			mode:    0644,
			modTime: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, f := range files {
		hdr := &tar.Header{
			Name:       f.name,
			Mode:       int64(f.mode),
			Uid:        f.uid,
			Gid:        f.gid,
			Size:       int64(len(f.content)),
			ModTime:    f.modTime,
			Typeflag:   f.typeflag,
			Uname:      "",
			Gname:      "",
			PAXRecords: nil,
		}

		if f.isDir {
			hdr.Typeflag = tar.TypeDir
			hdr.Size = 0
			hdr.Mode = int64(f.mode)
		}

		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}

		if !f.isDir {
			if _, err := tw.Write([]byte(f.content)); err != nil {
				t.Fatal(err)
			}
		}
	}

	return buf.Bytes()
}

func TestNew(t *testing.T) {
	tarData := createTestTar(t)
	tarFS, err := tarfs.New(bytes.NewReader(tarData))
	require.NoError(t, err)

	// Test file content
	files := []struct {
		path    string
		content string
	}{
		{"file1.txt", "Hello, World!"},
		{"dir1/file2.txt", "Hello from dir1!"},
		{"dir1/dir2/file3.txt", "Hello from dir2!"},
	}

	for _, f := range files {
		file, err := tarFS.Open(f.path)
		require.NoError(t, err, "Failed to open %s", f.path)
		defer file.Close()

		content, err := io.ReadAll(file)
		require.NoError(t, err, "Failed to read %s", f.path)
		assert.Equal(t, f.content, string(content), "Unexpected content for %s", f.path)
	}

	// Test directory listing
	dirs := []struct {
		path     string
		expected []string
	}{
		{".", []string{"dir1", "file1.txt"}},
		{"dir1", []string{"dir2", "file2.txt"}},
		{"dir1/dir2", []string{"file3.txt"}},
	}

	for _, d := range dirs {
		dir, err := tarFS.Open(d.path)
		require.NoError(t, err, "Failed to open directory %s", d.path)

		dirFile, ok := dir.(fs.ReadDirFile)
		require.True(t, ok, "Directory does not implement fs.ReadDirFile")

		dirEntries, err := dirFile.ReadDir(-1)
		require.NoError(t, err, "Failed to read directory %s", d.path)

		var names []string
		for _, entry := range dirEntries {
			names = append(names, entry.Name())
		}

		assert.Equal(t, len(d.expected), len(names), "Unexpected number of entries in %s", d.path)
		for i, name := range names {
			assert.Equal(t, d.expected[i], name, "Unexpected entry in %s", d.path)
		}
	}
}

func TestOpen(t *testing.T) {
	tarData := createTestTar(t)
	tarFS, err := tarfs.New(bytes.NewReader(tarData))
	require.NoError(t, err)

	tests := []struct {
		name        string
		path        string
		wantErr     bool
		errContains string
	}{
		{
			name: "existing file",
			path: "file1.txt",
		},
		{
			name:        "non-existent file",
			path:        "nonexistent.txt",
			wantErr:     true,
			errContains: "open nonexistent.txt: file does not exist",
		},
		{
			name:        "invalid path",
			path:        "",
			wantErr:     true,
			errContains: "open : file does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := tarFS.Open(tt.path)
			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, tt.errContains, err.Error())
				return
			}
			require.NoError(t, err)
			defer f.Close()
		})
	}
}

func TestFileRead(t *testing.T) {
	content := "Hello, World!"

	tarData := createTestTar(t)
	tarFS, err := tarfs.New(bytes.NewReader(tarData))
	require.NoError(t, err)

	f, err := tarFS.Open("file1.txt")
	require.NoError(t, err)
	defer f.Close()

	// Test reading entire file
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	assert.Equal(t, content, string(data))

	// Test seeking and reading
	seeker, ok := f.(io.Seeker)
	require.True(t, ok, "file does not implement io.Seeker")

	_, err = seeker.Seek(0, io.SeekStart)
	require.NoError(t, err)

	buf := make([]byte, 5)
	n, err := f.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 5, n, "unexpected number of bytes read")
	assert.Equal(t, "Hello", string(buf))
}

func TestFileReadDir(t *testing.T) {
	tarData := createTestTar(t)
	tarFS, err := tarfs.New(bytes.NewReader(tarData))
	require.NoError(t, err)

	dir, err := tarFS.Open("dir1")
	require.NoError(t, err)
	defer dir.Close()

	dirFile, ok := dir.(fs.ReadDirFile)
	require.True(t, ok, "directory does not implement fs.ReadDirFile")

	entries, err := dirFile.ReadDir(-1)
	require.NoError(t, err)

	expected := []string{"dir2", "file2.txt"}
	assert.Equal(t, len(expected), len(entries), "unexpected number of entries")

	for i, entry := range entries {
		assert.Equal(t, expected[i], entry.Name(), "unexpected entry name")
	}
}

func TestHeaderMethods(t *testing.T) {
	modTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	tarData := createTestTar(t)
	tarFS, err := tarfs.New(bytes.NewReader(tarData))
	require.NoError(t, err)

	f, err := tarFS.Open("file1.txt")
	require.NoError(t, err)
	defer f.Close()

	info, err := f.Stat()
	require.NoError(t, err)

	tests := []struct {
		name     string
		got      interface{}
		expected interface{}
	}{
		{"Name", info.Name(), "file1.txt"},
		{"Size", info.Size(), int64(13)},
		{"Mode", info.Mode(), fs.FileMode(0644)},
		{"ModTime", info.ModTime(), modTime},
		{"IsDir", info.IsDir(), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.got, "unexpected %s", tt.name)
		})
	}
}

func TestFSInterface(t *testing.T) {
	tarData := createTestTar(t)
	tarFS, err := tarfs.New(bytes.NewReader(tarData))
	require.NoError(t, err)

	err = fstest.TestFS(tarFS,
		"file1.txt",
		"dir1",
		"dir1/file2.txt",
		"dir1/dir2",
		"dir1/dir2/file3.txt",
	)
	require.NoError(t, err)
}
