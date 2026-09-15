// Package data embeds the public #93 corpus; no runtime fixture path is accepted.
package data

import _ "embed"

//go:embed cases.json
var Cases []byte
