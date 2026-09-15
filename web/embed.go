// Package web embeds the compiled provisional read-only shell (web/dist).
// Build it with `npm --prefix web ci --ignore-scripts && npm --prefix web run build`
// before the Go build; without index.html the daemon serves an explicit
// ui_unavailable refusal rather than a fallback runtime or dev server.
package web

import (
	"embed"
	"io/fs"
	"regexp"
)

//go:embed all:dist
var dist embed.FS

// files is swapped only by this package's tests to prove the unbuilt path.
var files fs.FS = dist

var assetName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}\.(js|css)$`)

// Asset returns the embedded file body and exact Content-Type for the request
// path, or ok=false. Only "/" and hashed /assets/<name>.(js|css) are servable.
func Asset(path string) (body []byte, contentType string, ok bool) {
	var name string
	switch {
	case path == "/":
		name, contentType = "index.html", "text/html; charset=utf-8"
	case len(path) > len("/assets/") && path[:len("/assets/")] == "/assets/" && assetName.MatchString(path[len("/assets/"):]):
		name = path[len("/assets/"):]
		if name[len(name)-3:] == ".js" {
			contentType = "text/javascript; charset=utf-8"
		} else {
			contentType = "text/css; charset=utf-8"
		}
		name = "assets/" + name
	default:
		return nil, "", false
	}
	body, err := fs.ReadFile(files, "dist/"+name)
	if err != nil {
		return nil, "", false
	}
	return body, contentType, true
}
