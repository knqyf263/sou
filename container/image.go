package container

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/daemon"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/knqyf263/sou/tarfs"
)

var debugLogger *log.Logger

func init() {
	// Open log file
	logFile, err := os.OpenFile("/tmp/lcat-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	debugLogger = log.New(logFile, "", log.LstdFlags)
}

func debug(format string, v ...interface{}) {
	if debugLogger != nil {
		debugLogger.Printf("sou/container: "+format, v...)
	}
}

// Image represents a container image
type Image struct {
	Reference string
	Layers    []Layer
	img       v1.Image
}

// Layer represents an image layer
type Layer struct {
	DiffID  string
	Size    int64
	Command string
	layer   v1.Layer
	fs      *tarfs.FS
}

// File represents a file in a layer
type File struct {
	Name    string
	IsDir   bool
	Path    string
	Size    int64
	Mode    string
	ModTime string
}

// ProgressFunc is a callback function to report progress
type ProgressFunc func(float64)

// NewImage creates a new Image instance from a reference
func NewImage(ref string, progress ProgressFunc) (*Image, bool, error) {
	reference, err := name.ParseReference(ref)
	if err != nil {
		return nil, false, fmt.Errorf("failed to parse reference: %w", err)
	}

	// Try to get the image from the local daemon first
	img, err := daemon.Image(reference)
	if err == nil {
		debug("Found local image")
		image, err := createImageFromV1(img, ref)
		if err != nil {
			debug("Failed to create image from local daemon: %v", err)
			return nil, false, err
		}
		debug("Successfully loaded local image, returning with isLocalImage=true")
		return image, true, nil
	}

	// If not found locally, try to pull from remote
	debug("Image not found locally, pulling from registry")
	fmt.Printf("Image not found locally. Pulling from registry...\n")

	progressChan := make(chan v1.Update, 100)
	go func() {
		var last float64
		for update := range progressChan {
			if update.Total > 0 {
				current := float64(update.Complete) / float64(update.Total)
				if current > last {
					progress(current)
					last = current
				}
			}
		}
	}()

	img, err = remote.Image(reference, remote.WithProgress(progressChan))
	if err != nil {
		debug("Failed to pull remote image: %v", err)
		return nil, false, fmt.Errorf("failed to pull image: %w", err)
	}

	close(progressChan)
	progress(1.0) // Ensure we show 100% completion
	image, err := createImageFromV1(img, ref)
	if err != nil {
		debug("Failed to create image from remote: %v", err)
		return nil, false, err
	}
	debug("Successfully pulled remote image")
	return image, false, nil
}

// createImageFromV1 creates an Image instance from a v1.Image
func createImageFromV1(img v1.Image, ref string) (*Image, error) {
	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("failed to get layers: %w", err)
	}

	configFile, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("failed to get config file: %w", err)
	}

	var imageLayers []Layer
	historyIndex := len(configFile.History) - 1
	nonEmptyLayers := 0

	// Count non-empty layers first
	for i := len(configFile.History) - 1; i >= 0; i-- {
		if !configFile.History[i].EmptyLayer {
			nonEmptyLayers++
		}
	}

	if nonEmptyLayers != len(layers) {
		return nil, fmt.Errorf("mismatch between number of layers (%d) and non-empty history entries (%d)", len(layers), nonEmptyLayers)
	}

	layerIndex := 0
	for historyIndex >= 0 && layerIndex < len(layers) {
		if configFile.History[historyIndex].EmptyLayer {
			historyIndex--
			continue
		}

		layer := layers[layerIndex]
		diffID, err := layer.DiffID()
		if err != nil {
			layerIndex++
			continue
		}

		size, err := layer.Size()
		if err != nil {
			layerIndex++
			continue
		}

		command := configFile.History[historyIndex].CreatedBy
		if command == "" {
			layerIndex++
			historyIndex--
			continue
		}

		imageLayers = append(imageLayers, Layer{
			DiffID:  diffID.String(),
			Size:    size,
			Command: command,
			layer:   layer,
		})

		layerIndex++
		historyIndex--
	}

	return &Image{
		Reference: ref,
		Layers:    imageLayers,
		img:       img,
	}, nil
}

// progressReader wraps an io.Reader to track progress
type progressReader struct {
	r          io.Reader
	total      int64
	current    int64
	progress   func(float64)
	lastUpdate time.Time
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 {
		pr.current += int64(n)
		if pr.total > 0 {
			now := time.Now()
			// Update progress at most once every 50ms
			if now.Sub(pr.lastUpdate) >= 50*time.Millisecond {
				progress := float64(pr.current) / float64(pr.total)
				if progress > 1.0 {
					progress = 1.0
				}
				// Scale progress to be between 0.0 and 1.0
				if pr.progress != nil {
					pr.progress(progress)
				}
				pr.lastUpdate = now
			}
		}
	}

	// Ensure we send the final progress when the read is complete
	if err == io.EOF && pr.current > 0 && pr.total > 0 && pr.progress != nil {
		pr.progress(1.0) // Final progress
	}

	return n, err
}

// initializeFromCache attempts to initialize the layer from cache
// Returns true if successful, false if cache miss or error
func (l *Layer) initializeFromCache(progress func(float64)) (bool, error) {
	cachedPath := getCachedLayer(l.DiffID)
	if cachedPath == "" {
		return false, nil
	}

	debug("InitializeLayer: Found cached layer at %s", cachedPath)
	file, err := os.Open(cachedPath)
	if err != nil {
		debug("InitializeLayer: Failed to open cached file: %v", err)
		return false, nil // Treat as cache miss
	}
	defer func() {
		if l.fs == nil {
			file.Close() // Only close if initialization failed
		}
	}()

	progress(0.5)
	debug("InitializeLayer: Creating tarfs from cache")
	tfs, err := tarfs.New(file)
	if err != nil {
		debug("InitializeLayer: Failed to create tarfs from cache: %v", err)
		return false, nil // Treat as cache miss
	}

	l.fs = tfs
	progress(1.0)
	debug("InitializeLayer: Successfully loaded from cache")
	return true, nil
}

// createNewLayer creates a new layer from the uncompressed content
func (l *Layer) createNewLayer(progress func(float64)) error {
	tmpFile, err := getCacheFilePath()
	if err != nil {
		return fmt.Errorf("failed to get cache file path: %w", err)
	}
	debug("InitializeLayer: Created temp file at %s", tmpFile)

	file, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("failed to create cache file: %w", err)
	}
	defer func() {
		if l.fs == nil {
			file.Close() // Only close if initialization failed
		}
	}()

	progress(0.2)
	debug("InitializeLayer: Getting layer content")

	rc, err := l.layer.Uncompressed()
	if err != nil {
		return fmt.Errorf("failed to get layer content: %w", err)
	}
	defer rc.Close()

	size, err := l.layer.Size()
	if err != nil {
		return fmt.Errorf("failed to get layer size: %w", err)
	}
	debug("InitializeLayer: Layer size: %d bytes", size)

	pr := &progressReader{
		r:          rc,
		total:      size,
		progress:   progress,
		lastUpdate: time.Now(),
	}

	debug("InitializeLayer: Copying layer content")
	if _, err := io.Copy(file, pr); err != nil {
		return fmt.Errorf("failed to copy layer content: %w", err)
	}

	progress(0.8)
	debug("InitializeLayer: Content copied successfully")

	if _, err := file.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek cache file: %w", err)
	}

	debug("InitializeLayer: Creating tarfs")
	tfs, err := tarfs.New(file)
	if err != nil {
		return fmt.Errorf("failed to create tarfs: %w", err)
	}

	cacheLayer(l.DiffID, tmpFile)
	l.fs = tfs
	progress(1.0)
	debug("InitializeLayer: Layer initialization completed successfully")

	return nil
}

// InitializeLayer prepares the layer filesystem with progress reporting
func (l *Layer) InitializeLayer(progress func(float64)) error {
	debug("InitializeLayer: Starting initialization for layer %s", l.DiffID)

	if l.fs != nil {
		debug("InitializeLayer: Layer already initialized")
		progress(1.0)
		return nil
	}

	// Report start of loading
	progress(0.0)
	debug("InitializeLayer: Checking cache")

	// Try to initialize from cache first
	if ok, _ := l.initializeFromCache(progress); ok {
		return nil
	}

	// If cache initialization failed, create new layer
	return l.createNewLayer(progress)
}

// GetFiles returns files in the specified path
func (l *Layer) GetFiles(path string) ([]File, error) {
	if l.fs == nil {
		return nil, fmt.Errorf("layer not initialized")
	}

	// Open the directory
	dir, err := l.fs.Open(path)
	if err != nil {
		return nil, err
	}
	defer dir.Close()

	// Read directory entries
	dirFile, ok := dir.(fs.ReadDirFile)
	if !ok {
		return nil, fmt.Errorf("not a directory")
	}

	entries, err := dirFile.ReadDir(-1)
	if err != nil {
		return nil, err
	}

	var files []File
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}

		files = append(files, File{
			Name:    entry.Name(),
			IsDir:   entry.IsDir(),
			Path:    filepath.Join(path, entry.Name()),
			Size:    info.Size(),
			Mode:    info.Mode().String(),
			ModTime: info.ModTime().Format("2006-01-02 15:04:05"),
		})
	}

	return files, nil
}

// ReadFile reads the content of a file in the layer
func (l *Layer) ReadFile(path string) ([]byte, error) {
	if l.fs == nil {
		return nil, fmt.Errorf("layer not initialized")
	}

	file, err := l.fs.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return content, nil
}

// GetManifest returns the image manifest
func (i *Image) GetManifest() ([]byte, error) {
	return i.GetManifestWithColor(true)
}

// GetManifestWithColor returns the image manifest with optional color
func (i *Image) GetManifestWithColor(colored bool) ([]byte, error) {
	manifest, err := i.img.Manifest()
	if err != nil {
		return nil, fmt.Errorf("failed to get manifest: %w", err)
	}
	jsonBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal manifest: %w", err)
	}
	return jsonBytes, nil
}

// GetConfig returns the image config
func (i *Image) GetConfig() ([]byte, error) {
	return i.GetConfigWithColor(true)
}

// GetConfigWithColor returns the image config with optional color
func (i *Image) GetConfigWithColor(colored bool) ([]byte, error) {
	config, err := i.img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}
	jsonBytes, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}
	return jsonBytes, nil
}
