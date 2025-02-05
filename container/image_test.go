package container

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/daemon"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// mockProgressFunc is a mock progress function for testing
func mockProgressFunc(progress float64) {}

// setupTestImage creates a random image for testing
func setupTestImage(t *testing.T) (v1.Image, error) {
	return random.Image(1024, 3)
}

// setupTestRegistry creates a test registry server and returns its URL
func setupTestRegistry(t *testing.T) string {
	s := httptest.NewServer(registry.New())
	t.Cleanup(func() {
		s.Close()
	})
	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatalf("Failed to parse server URL: %v", err)
	}
	return u.Host
}

// createTestLayer creates a layer with test content
func createTestLayer(t *testing.T) (v1.Layer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Add a test file
	content := []byte("test content")
	if err := tw.WriteHeader(&tar.Header{
		Name:     filepath.Join(".", "test.txt"), // Use relative path
		Size:     int64(len(content)),
		Mode:     0644,
		Typeflag: tar.TypeReg,
	}); err != nil {
		return nil, fmt.Errorf("failed to write header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		return nil, fmt.Errorf("failed to write content: %v", err)
	}

	// Add a directory
	if err := tw.WriteHeader(&tar.Header{
		Name:     filepath.Join(".", "testdir"), // Use relative path
		Mode:     0755,
		Typeflag: tar.TypeDir,
	}); err != nil {
		return nil, fmt.Errorf("failed to write directory header: %v", err)
	}

	// Add a file in the directory
	dirContent := []byte("directory test content")
	if err := tw.WriteHeader(&tar.Header{
		Name:     filepath.Join(".", "testdir", "file.txt"), // Use relative path
		Size:     int64(len(dirContent)),
		Mode:     0644,
		Typeflag: tar.TypeReg,
	}); err != nil {
		return nil, fmt.Errorf("failed to write header: %v", err)
	}
	if _, err := tw.Write(dirContent); err != nil {
		return nil, fmt.Errorf("failed to write content: %v", err)
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("failed to close tar writer: %v", err)
	}

	return tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
	})
}

// setupFakeImage creates a fake image for testing with specific layers
func setupFakeImage(t *testing.T) v1.Image {
	img, err := random.Image(1024, 1)
	if err != nil {
		t.Fatalf("Failed to create random image: %v", err)
	}

	testLayer, err := createTestLayer(t)
	if err != nil {
		t.Fatalf("Failed to create test layer: %v", err)
	}

	img, err = mutate.AppendLayers(img, testLayer)
	if err != nil {
		t.Fatalf("Failed to append test layer: %v", err)
	}

	// Add history to the config
	cfg, err := img.ConfigFile()
	if err != nil {
		t.Fatalf("Failed to get config file: %v", err)
	}

	cfg.History = []v1.History{
		{
			Created:   v1.Time{Time: time.Now()},
			CreatedBy: "test command 1",
		},
	}

	return img
}

// createTempDir creates a temporary directory for testing
func createTempDir(t *testing.T) string {
	dir, err := os.MkdirTemp("", "image-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		os.RemoveAll(dir)
	})
	return dir
}

func TestNewImage(t *testing.T) {
	t.Run("remote image", func(t *testing.T) {
		registryHost := setupTestRegistry(t)

		// Create and push a test image
		img, err := setupTestImage(t)
		if err != nil {
			t.Fatalf("Failed to setup test image: %v", err)
		}

		ref := fmt.Sprintf("%s/test/image:latest", registryHost)
		imgRef, err := name.ParseReference(ref)
		if err != nil {
			t.Fatalf("Failed to parse reference: %v", err)
		}

		if err := remote.Write(imgRef, img); err != nil {
			t.Fatalf("Failed to push image: %v", err)
		}

		// Test with the pushed image
		image, isLocal, err := NewImage(ref, mockProgressFunc)
		if err != nil {
			t.Errorf("NewImage() error = %v", err)
			return
		}

		if isLocal {
			t.Error("Expected isLocal to be false")
		}

		if image.Reference != ref {
			t.Errorf("Expected reference %s, got %s", ref, image.Reference)
		}
	})

	t.Run("local image", func(t *testing.T) {
		// Create a local image using daemon
		img, err := random.Image(1024, 1)
		if err != nil {
			t.Fatalf("Failed to create random image: %v", err)
		}

		ref := "test/local-image:latest"
		tag, err := name.NewTag(ref)
		if err != nil {
			t.Fatalf("Failed to create tag: %v", err)
		}

		if _, err := daemon.Write(tag, img); err != nil {
			// Skip test if daemon is not available
			t.Skipf("daemon not available: %v", err)
		}

		image, isLocal, err := NewImage(ref, mockProgressFunc)
		if err != nil {
			t.Errorf("NewImage() error = %v", err)
			return
		}

		if !isLocal {
			t.Error("Expected isLocal to be true")
		}

		if image.Reference != ref {
			t.Errorf("Expected reference %s, got %s", ref, image.Reference)
		}
	})

	t.Run("invalid reference", func(t *testing.T) {
		_, _, err := NewImage("invalid:@reference", mockProgressFunc)
		if err == nil {
			t.Error("Expected error for invalid reference")
		}
	})

	t.Run("non-existent image", func(t *testing.T) {
		_, _, err := NewImage("nonexistent/image:latest", mockProgressFunc)
		if err == nil {
			t.Error("Expected error for non-existent image")
		}
	})
}

func TestInitializeLayer(t *testing.T) {
	layer, err := createTestLayer(t)
	if err != nil {
		t.Fatalf("Failed to create test layer: %v", err)
	}

	l := Layer{
		layer: layer,
	}

	err = l.InitializeLayer(mockProgressFunc)
	if err != nil {
		t.Errorf("InitializeLayer() error = %v", err)
		return
	}

	if l.fs == nil {
		t.Error("Expected layer.fs to be initialized")
	}
}

func TestGetFiles(t *testing.T) {
	layer, err := createTestLayer(t)
	if err != nil {
		t.Fatalf("Failed to create test layer: %v", err)
	}

	l := Layer{
		layer: layer,
	}

	err = l.InitializeLayer(mockProgressFunc)
	if err != nil {
		t.Fatalf("Failed to initialize layer: %v", err)
	}

	// Test root directory
	files, err := l.GetFiles(".")
	if err != nil {
		t.Errorf("GetFiles('.') error = %v", err)
		return
	}

	if len(files) != 2 { // test.txt and testdir
		t.Errorf("Expected 2 files in root, got %d", len(files))
	}

	// Verify file contents
	var foundFile, foundDir bool
	for _, f := range files {
		switch f.Name {
		case "test.txt":
			foundFile = true
			if f.IsDir {
				t.Error("Expected test.txt to be a file")
			}
		case "testdir":
			foundDir = true
			if !f.IsDir {
				t.Error("Expected testdir to be a directory")
			}
		}
	}

	if !foundFile {
		t.Error("Expected to find test.txt")
	}
	if !foundDir {
		t.Error("Expected to find testdir")
	}

	// Test subdirectory
	files, err = l.GetFiles("testdir")
	if err != nil {
		t.Errorf("GetFiles('testdir') error = %v", err)
		return
	}

	if len(files) != 1 { // file.txt
		t.Errorf("Expected 1 file in testdir, got %d", len(files))
	}

	if files[0].Name != "file.txt" {
		t.Errorf("Expected file.txt in testdir, got %s", files[0].Name)
	}
}

func TestReadFile(t *testing.T) {
	layer, err := createTestLayer(t)
	if err != nil {
		t.Fatalf("Failed to create test layer: %v", err)
	}

	l := Layer{
		layer: layer,
	}

	err = l.InitializeLayer(mockProgressFunc)
	if err != nil {
		t.Fatalf("Failed to initialize layer: %v", err)
	}

	// Test reading existing file in root
	content, err := l.ReadFile("test.txt")
	if err != nil {
		t.Errorf("ReadFile('test.txt') error = %v", err)
		return
	}

	if string(content) != "test content" {
		t.Errorf("Expected content 'test content', got '%s'", string(content))
	}

	// Test reading existing file in subdirectory
	content, err = l.ReadFile(filepath.Join("testdir", "file.txt"))
	if err != nil {
		t.Errorf("ReadFile('testdir/file.txt') error = %v", err)
		return
	}

	if string(content) != "directory test content" {
		t.Errorf("Expected content 'directory test content', got '%s'", string(content))
	}

	// Test reading non-existent file
	_, err = l.ReadFile("nonexistent")
	if err == nil {
		t.Error("Expected error when reading non-existent file")
	}
}

func TestGetManifest(t *testing.T) {
	img, err := setupTestImage(t)
	if err != nil {
		t.Fatalf("Failed to setup test image: %v", err)
	}

	image := &Image{
		img: img,
	}

	manifest, err := image.GetManifest()
	if err != nil {
		t.Errorf("GetManifest() error = %v", err)
		return
	}

	var m map[string]interface{}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Errorf("Failed to unmarshal manifest: %v", err)
	}
}

func TestGetConfig(t *testing.T) {
	img, err := setupTestImage(t)
	if err != nil {
		t.Fatalf("Failed to setup test image: %v", err)
	}

	image := &Image{
		img: img,
	}

	config, err := image.GetConfig()
	if err != nil {
		t.Errorf("GetConfig() error = %v", err)
		return
	}

	var c map[string]interface{}
	if err := json.Unmarshal(config, &c); err != nil {
		t.Errorf("Failed to unmarshal config: %v", err)
	}
}

func TestCleanupCache(t *testing.T) {
	// Create a temporary cache directory
	tmpDir, err := os.MkdirTemp("", "image-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Save original cache directory and restore it after test
	originalCacheDir := cacheDir
	t.Cleanup(func() {
		cacheDir = originalCacheDir
		os.RemoveAll(tmpDir)
	})

	cacheDir = tmpDir

	// Create some test files in the cache directory
	testFiles := []string{
		"test1.tar",
		"test2.tar",
		filepath.Join("subdir", "test3.tar"),
	}

	for _, f := range testFiles {
		path := filepath.Join(tmpDir, f)
		if filepath.Dir(path) != tmpDir {
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatalf("Failed to create directory: %v", err)
			}
		}
		if err := os.WriteFile(path, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Add files to the cache map
	for i, f := range testFiles {
		cacheLayer(fmt.Sprintf("sha256:test%d", i), filepath.Join(tmpDir, f))
	}

	// Verify that test files were created
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to read cache directory: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("Expected test files to be created")
	}

	// Run cleanup
	if err := CleanupCache(); err != nil {
		t.Errorf("CleanupCache() error = %v", err)
	}

	// Test cleanup when cache directory is empty
	if err := CleanupCache(); err != nil {
		t.Errorf("CleanupCache() error = %v", err)
	}
}
