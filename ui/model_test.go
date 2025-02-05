package ui

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/knqyf263/sou/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// setupTestImage creates a test image with a single layer
func setupTestImage(t *testing.T) (*container.Image, error) {
	registryHost := setupTestRegistry(t)

	// Create a random base image
	baseImg, err := random.Image(1024, 1)
	if err != nil {
		return nil, err
	}

	// Create a test layer
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	content := []byte("test content")
	if err := tw.WriteHeader(&tar.Header{
		Name:     "test.txt",
		Size:     int64(len(content)),
		Mode:     0644,
		Typeflag: tar.TypeReg,
		ModTime:  time.Now(),
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(content); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}

	layer, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
	})
	if err != nil {
		return nil, err
	}

	// Add the test layer to the image
	img, err := mutate.AppendLayers(baseImg, layer)
	if err != nil {
		return nil, err
	}

	// Add history to the config
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}

	cfg.History = append(cfg.History, v1.History{
		Created:   v1.Time{Time: time.Now()},
		CreatedBy: "test command",
	})

	img, err = mutate.Config(img, cfg.Config)
	if err != nil {
		return nil, err
	}

	// Create a reference for the test registry
	ref := fmt.Sprintf("%s/test/image:latest", registryHost)
	imgRef, err := name.ParseReference(ref)
	if err != nil {
		return nil, err
	}

	// Push the image to test registry
	if err := remote.Write(imgRef, img); err != nil {
		return nil, err
	}

	// Load the image using container.NewImage
	image, _, err := container.NewImage(ref, func(float64) {})
	if err != nil {
		return nil, err
	}

	return image, nil
}

func TestNewModel(t *testing.T) {
	registryHost := setupTestRegistry(t)

	// Create and push a test image
	img, err := random.Image(1024, 1)
	require.NoError(t, err)

	ref := fmt.Sprintf("%s/test/valid:latest", registryHost)
	imgRef, err := name.ParseReference(ref)
	require.NoError(t, err)

	err = remote.Write(imgRef, img)
	require.NoError(t, err)

	tests := []struct {
		name    string
		ref     string
		wantErr bool
	}{
		{
			name:    "valid reference",
			ref:     ref,
			wantErr: false,
		},
		{
			name:    "invalid reference",
			ref:     "invalid:@reference",
			wantErr: true,
		},
		{
			name:    "non-existent image",
			ref:     fmt.Sprintf("%s/test/nonexistent:latest", registryHost),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, cmd := NewModel(tt.ref)
			if tt.wantErr {
				assert.NotNil(t, cmd)
				msg := cmd()
				switch m := msg.(type) {
				case errMsg:
					assert.Error(t, m.err)
				case tea.BatchMsg:
					foundError := false
					for _, c := range m {
						if errMsg, ok := c().(errMsg); ok {
							assert.Error(t, errMsg.err)
							foundError = true
							break
						}
					}
					if !foundError {
						t.Error("Expected error message not found in batch")
					}
				default:
					t.Errorf("Unexpected message type: %T", msg)
				}
			} else {
				assert.NotNil(t, model)
				assert.NotNil(t, cmd)
			}
		})
	}
}

func TestModelUpdate(t *testing.T) {
	img, err := setupTestImage(t)
	require.NoError(t, err)

	tests := []struct {
		name        string
		initialMode Mode
		msg         tea.Msg
		wantMode    Mode
	}{
		{
			name:        "quit message",
			initialMode: LayerMode,
			msg:         tea.KeyMsg{Type: tea.KeyCtrlC},
			wantMode:    LayerMode,
		},
		{
			name:        "window size message",
			initialMode: LayerMode,
			msg:         tea.WindowSizeMsg{Width: 100, Height: 50},
			wantMode:    LayerMode,
		},
		{
			name:        "loading layer message success",
			initialMode: LoadingMode,
			msg: loadingLayerMsg{
				layer: &img.Layers[0],
				err:   nil,
			},
			wantMode: LoadingMode,
		},
		{
			name:        "loading layer message error",
			initialMode: LoadingMode,
			msg: loadingLayerMsg{
				layer: nil,
				err:   assert.AnError,
			},
			wantMode: LayerMode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := Model{
				mode: tt.initialMode,
				list: list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0),
				keys: newKeyMap(),
			}
			updatedModel, _ := model.Update(tt.msg)
			assert.Equal(t, tt.wantMode, updatedModel.(Model).mode)
		})
	}
}

func TestModelView(t *testing.T) {
	tests := []struct {
		name     string
		model    Model
		wantView bool
	}{
		{
			name: "not ready",
			model: Model{
				ready: false,
			},
			wantView: true,
		},
		{
			name: "ready layer mode",
			model: Model{
				ready:  true,
				mode:   LayerMode,
				width:  100,
				height: 50,
			},
			wantView: true,
		},
		{
			name: "ready loading mode",
			model: Model{
				ready:  true,
				mode:   LoadingMode,
				width:  100,
				height: 50,
			},
			wantView: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := tt.model.View()
			if tt.wantView {
				assert.NotEmpty(t, view)
			} else {
				assert.Empty(t, view)
			}
		})
	}
}

func TestShowFiles(t *testing.T) {
	img, err := setupTestImage(t)
	require.NoError(t, err)

	// Initialize the layer
	err = img.Layers[0].InitializeLayer(func(float64) {})
	require.NoError(t, err)

	tests := []struct {
		name    string
		layer   *container.Layer
		path    string
		wantErr bool
	}{
		{
			name:    "nil layer",
			layer:   nil,
			path:    "/",
			wantErr: true,
		},
		{
			name:    "valid layer and path",
			layer:   &img.Layers[0],
			path:    "/",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{
				list: list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0),
				keys: newKeyMap(),
			}
			err := m.showFiles(tt.layer, tt.path)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.layer, m.currentLayer)
				assert.Equal(t, tt.path, m.currentPath)
				assert.NotEmpty(t, m.list.Items())
			}
		})
	}
}

func TestColorizeJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty input",
			input: "",
			want:  "\n",
		},
		{
			name: "simple json",
			input: `{
  "key": "value"
}`,
			want: "\x1b[33m{\x1b[0m\n  \"\x1b[36mkey\x1b[0m\": \x1b[32m\"value\"\x1b[0m\n\x1b[33m}\x1b[0m\n",
		},
		{
			name: "complex json",
			input: `{
  "string": "value",
  "number": 123,
  "bool": true,
  "object": {},
  "array": []
}`,
			want: "\x1b[33m{\x1b[0m\n  \"\x1b[36mstring\x1b[0m\": \x1b[32m\"value\",\x1b[0m\n  \"\x1b[36mnumber\x1b[0m\": \x1b[34m123,\x1b[0m\n  \"\x1b[36mbool\x1b[0m\": true,\n  \"\x1b[36mobject\x1b[0m\": \x1b[33m{},\x1b[0m\n  \"\x1b[36marray\x1b[0m\": \x1b[33m[]\x1b[0m\n\x1b[33m}\x1b[0m\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(colorizeJSON([]byte(tt.input)))
			assert.Equal(t, tt.want, got)
		})
	}
}
