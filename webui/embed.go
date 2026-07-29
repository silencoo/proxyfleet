package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var embedded embed.FS

var files, _ = fs.Sub(embedded, "dist")

// ReadFile returns one production WebUI asset from the Vite dist tree.
func ReadFile(name string) ([]byte, error) {
	return fs.ReadFile(files, name)
}

// Files exposes the immutable production asset tree for tests and HTTP serving.
func Files() fs.FS { return files }
