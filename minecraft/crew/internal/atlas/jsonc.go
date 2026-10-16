package atlas

import (
	"bytes"
	"encoding/json"
)

// stripJSONComments removes // and /* */ comments and trailing commas. Mojang
// ships its pack JSON with both, which encoding/json rejects. The scan is
// string-aware so URLs and comment-like text inside values survive.
func stripJSONComments(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); {
		c := in[i]
		switch {
		case c == '"':
			start := i
			i++
			for i < len(in) && in[i] != '"' {
				if in[i] == '\\' {
					i++
				}
				i++
			}
			i = min(i+1, len(in))
			out = append(out, in[start:i]...)
		case c == '/' && i+1 < len(in) && in[i+1] == '/':
			for i < len(in) && in[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(in) && in[i+1] == '*':
			end := bytes.Index(in[i+2:], []byte("*/"))
			if end < 0 {
				i = len(in)
			} else {
				i += end + 4
			}
			out = append(out, ' ')
		default:
			out = append(out, c)
			i++
		}
	}
	return dropTrailingCommas(out)
}

func dropTrailingCommas(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		c := in[i]
		if c == '"' {
			start := i
			i++
			for i < len(in) && in[i] != '"' {
				if in[i] == '\\' {
					i++
				}
				i++
			}
			end := min(i+1, len(in))
			out = append(out, in[start:end]...)
			i = end - 1
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(in) && (in[j] == ' ' || in[j] == '\t' || in[j] == '\n' || in[j] == '\r') {
				j++
			}
			if j < len(in) && (in[j] == '}' || in[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

func unmarshalLoose(data []byte, v any) error {
	return json.Unmarshal(stripJSONComments(data), v)
}
