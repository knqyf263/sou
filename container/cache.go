package container

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	cacheDir     string
	cacheDirOnce sync.Once
	cacheMutex   sync.RWMutex
	layerCache   = make(map[string]string) // DiffID -> cache file path
)

// initCacheDir initializes the cache directory
func initCacheDir() error {
	var err error
	cacheDirOnce.Do(func() {
		// Create a temporary directory for the cache
		cacheDir, err = os.MkdirTemp("", "sou-cache-*")
		if err != nil {
			err = fmt.Errorf("failed to create cache directory: %w", err)
			return
		}
	})
	return err
}

// getCachedLayer returns the cached layer file path if it exists
func getCachedLayer(diffID string) string {
	cacheMutex.RLock()
	defer cacheMutex.RUnlock()
	return layerCache[diffID]
}

// cacheLayer caches the layer file
func cacheLayer(diffID, filePath string) {
	cacheMutex.Lock()
	defer cacheMutex.Unlock()
	layerCache[diffID] = filePath
}

// CleanupCache removes all cached files and the cache directory
func CleanupCache() error {
	if cacheDir == "" {
		return nil
	}

	// Remove all cached files
	cacheMutex.Lock()
	defer cacheMutex.Unlock()

	for _, path := range layerCache {
		if err := os.Remove(path); err != nil {
			// Continue even if there's an error
			fmt.Fprintf(os.Stderr, "failed to remove cached file %s: %v\n", path, err)
		}
	}

	// Clear the cache map
	layerCache = make(map[string]string)

	// Remove the cache directory
	if err := os.RemoveAll(cacheDir); err != nil {
		return fmt.Errorf("failed to remove cache directory: %w", err)
	}

	return nil
}

// getCacheFilePath returns a new cache file path
func getCacheFilePath() (string, error) {
	if err := initCacheDir(); err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, fmt.Sprintf("layer-%d.tar", len(layerCache))), nil
}
