package workflow

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"slices"
	"strings"

	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/closedjson"
)

// Settings are data, not executable commands or permission to expand task scope.
// Preserve optional fields while sorting keys: the runner hashes the same bytes.
func normalizeSettings(harness string, raw json.RawMessage) (json.RawMessage, error) {
	refuse := func() (json.RawMessage, error) { return nil, g.Deny("malformed", "settings") }
	if len(raw) == 0 || len(raw) > 49152 {
		return refuse()
	}
	switch harness {
	case "fake":
		var spec struct {
			Attempts []struct {
				Mode  string `json:"mode"`
				Edits []struct {
					Path    string  `json:"path"`
					Content *string `json:"content,omitempty"`
					Base64  *string `json:"content_base64,omitempty"`
					Delete  bool    `json:"delete,omitempty"`
				} `json:"edits"`
				DelayMS     int64 `json:"delay_ms,omitempty"`
				StreamBytes int64 `json:"stream_bytes,omitempty"`
			} `json:"attempts"`
		}
		if closedjson.Decode(raw, &spec, 49152, nil) != nil || len(spec.Attempts) == 0 || len(spec.Attempts) > 128 {
			return refuse()
		}
		for _, a := range spec.Attempts {
			if !slices.Contains([]string{"noop", "edit", "delete", "create_empty", "binary_edit", "crash", "hang", "huge_output", "approval", "ignore_term", "exit_nonzero", "fork_child", "detached_child", "crash_after_edit"}, a.Mode) || a.DelayMS < 0 || a.DelayMS > 3600000 || a.StreamBytes < 0 || a.StreamBytes > 1<<30 || a.Edits == nil || len(a.Edits) > 128 {
				return refuse()
			}
			seen := map[string]bool{}
			for _, e := range a.Edits {
				if !fs.ValidPath(e.Path) || e.Path == "." || len(e.Path) > 512 || strings.ContainsAny(e.Path, "\\\x00\r\n\t") || seen[e.Path] {
					return refuse()
				}
				for _, part := range strings.Split(e.Path, "/") {
					if strings.EqualFold(part, ".git") {
						return refuse()
					}
				}
				seen[e.Path] = true
				values := 0
				if e.Content != nil {
					values++
				}
				if e.Base64 != nil {
					values++
					if _, err := base64.StdEncoding.Strict().DecodeString(*e.Base64); err != nil {
						return refuse()
					}
				}
				if e.Delete {
					values++
				}
				if values != 1 {
					return refuse()
				}
			}
		}
	case "opencode":
		var spec struct {
			Model   string `json:"model"`
			Variant string `json:"variant,omitempty"`
		}
		if closedjson.Decode(raw, &spec, 49152, nil) != nil || spec.Model == "" || len(spec.Model) > 256 || len(spec.Variant) > 256 || strings.ContainsAny(spec.Model+spec.Variant, "\x00\r\n\t") {
			return refuse()
		}
	default:
		return nil, g.Deny("malformed", "harness")
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&value) != nil {
		return refuse()
	}
	return json.Marshal(value)
}
