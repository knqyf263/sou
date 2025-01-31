package tarfs_test

import (
	"testing"
)

func TestWalkDir(t *testing.T) {
	//f, err := os.Open("/tmp/hard/alpine.tar")
	//require.NoError(t, err)
	//defer f.Close()
	//
	//tfs, err := tarfs.New(f)
	//require.NoError(t, err)
	//
	//err = fs.WalkDir(tfs, ".", func(path string, d fs.DirEntry, err error) error {
	//	require.NoError(t, err)
	//	if !d.SrcType().IsRegular() {
	//		return nil
	//	}
	//	if path == "/lib/apk/db/installed" {
	//		f, err := tfs.Open(path)
	//		require.NoError(t, err)
	//		defer f.Close()
	//
	//		b, err := io.ReadAll(f)
	//		require.NoError(t, err)
	//
	//		fmt.Println(string(b))
	//	}
	//	return nil
	//})
	//require.NoError(t, err)
}
