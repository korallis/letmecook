package web

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestAssetPaths(t *testing.T) {
	restore := files
	defer func() { files = restore }()
	files = fstest.MapFS{"dist/index.html": {Data: []byte("<!doctype html>")}, "dist/assets/index-abc.js": {Data: []byte("x")}, "dist/assets/index-abc.css": {Data: []byte("y")}, "dist/assets/notes.txt": {Data: []byte("z")}}
	for path, want := range map[string]string{"/": "text/html; charset=utf-8", "/assets/index-abc.js": "text/javascript; charset=utf-8", "/assets/index-abc.css": "text/css; charset=utf-8"} {
		if _, ct, ok := Asset(path); !ok || ct != want {
			t.Fatal(path, ct, ok)
		}
	}
	for _, path := range []string{"", "/index.html", "/dist/index.html", "/assets/", "/assets/notes.txt", "/assets/../index.html", "/assets/./index-abc.js", "/assets/a/b.js", "/assets/" + strings.Repeat("a", 65) + ".js", "/assets/index-abc.JS", "/assets/index-abc.js/", "/assets/missing.js", "/api/v1/status"} {
		if _, _, ok := Asset(path); ok {
			t.Fatal("served", path)
		}
	}
	files = fstest.MapFS{}
	if _, _, ok := Asset("/"); ok {
		t.Fatal("unbuilt shell served")
	}
}
