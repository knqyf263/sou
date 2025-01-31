package tarfs

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"time"
)

type FS struct {
	file    *os.File
	fileMap map[string]*Entry
}

type Header struct {
	typeflag byte
	name     string
	linkname string
	size     int64
	mode     fs.FileMode
	modTime  time.Time
}

func (h *Header) Name() string {
	return path.Base(h.name)
}

func (h *Header) Size() int64 {
	return h.size
}

func (h *Header) Mode() fs.FileMode {
	return h.mode
}

func (h *Header) ModTime() time.Time {
	return h.modTime
}

func (h *Header) IsDir() bool {
	return h.typeflag == tar.TypeDir
}

func (h *Header) Type() fs.FileMode {
	switch h.typeflag {
	case tar.TypeReg, tar.TypeRegA:
		return 0
	case tar.TypeLink:
		return 0 // Treat hard links as regular files
	case tar.TypeSymlink:
		return fs.ModeSymlink
	case tar.TypeChar:
		return fs.ModeDevice | fs.ModeCharDevice
	case tar.TypeBlock:
		return fs.ModeDevice
	case tar.TypeDir:
		return fs.ModeDir
	case tar.TypeFifo:
		return fs.ModeNamedPipe
	case tar.TypeCont, tar.TypeXHeader, tar.TypeXGlobalHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
		return fs.ModeIrregular
	default:
		return fs.ModeIrregular // Other types are treated as irregular files
	}
}

func (h *Header) Sys() any {
	return h
}

type Entry struct {
	Header   *Header
	Offset   int64
	Size     int64
	Children []*Entry
}

func New(file *os.File) (*FS, error) {
	tarfs := &FS{
		file: file,
		fileMap: map[string]*Entry{
			// pseudo root
			".": {
				Header: &Header{
					typeflag: tar.TypeDir,
					mode:     fs.ModeDir | fs.ModePerm,
				},
			},
		},
	}

	tr := tar.NewReader(file)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}

		// Get the current position
		pos, err := file.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, err
		}

		filePath := path.Clean(hdr.Name)
		entry := &Entry{
			Header: &Header{
				typeflag: hdr.Typeflag,
				name:     filePath,
				linkname: hdr.Linkname,
				size:     hdr.Size,
				mode:     fs.FileMode(hdr.Mode),
				modTime:  hdr.ModTime,
			},
			Offset: pos,
			Size:   hdr.Size,
		}

		tarfs.fileMap[filePath] = entry

		parentDir := path.Dir(filePath)
		if parentEntry, exists := tarfs.fileMap[parentDir]; exists {
			parentEntry.Children = append(parentEntry.Children, entry)
		}

	}

	return tarfs, nil
}

func (tfs *FS) Open(name string) (fs.File, error) {
	entry, ok := tfs.fileMap[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	if entry.Header.typeflag == tar.TypeLink {
		// Resolve hard link to target file
		linkname := entry.Header.linkname
		targetEntry, ok := tfs.fileMap[linkname]
		if !ok {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fmt.Errorf("target file %s not found", linkname)}
		}
		entry = targetEntry // Update entry to point to the target file
	}

	sr := io.NewSectionReader(tfs.file, entry.Offset, entry.Size)

	return &File{
		Header:   entry.Header,
		r:        sr,
		children: entry.Children,
	}, nil
}

type File struct {
	*Header  // Implement fs.FileInfo
	r        io.Reader
	children []*Entry
}

func (f *File) Stat() (fs.FileInfo, error) {
	return f, nil
}

func (f *File) Read(p []byte) (n int, err error) {
	return f.r.Read(p)
}

func (f *File) Close() error {
	// No need to close tar.Reader, it does not own the underlying io.Reader
	return nil
}

func (f *File) ReadDir(n int) ([]fs.DirEntry, error) {
	if !f.IsDir() {
		return nil, &fs.PathError{Op: "readdir", Path: f.name, Err: fs.ErrInvalid}
	}

	if n <= 0 || len(f.children) < n {
		n = len(f.children)
	}

	var entries []fs.DirEntry
	for _, childEntry := range f.children[:n] {
		entries = append(entries, &DirEntry{Header: childEntry.Header})
	}

	return entries, nil
}

type DirEntry struct {
	*Header
}

func (f *DirEntry) Info() (fs.FileInfo, error) {
	return f, nil
}
