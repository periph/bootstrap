package fat

import (
	"testing"

	"github.com/maruel/go-fs"
)

func TestFileSystemImplementsFileSystem(t *testing.T) {
	var raw interface{}
	raw = new(FileSystem)
	if _, ok := raw.(fs.FileSystem); !ok {
		t.Fatal("FileSystem should be a FileSystem")
	}
}
