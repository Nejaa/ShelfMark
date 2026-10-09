package library

import (
	"encoding/json"
	"strings"
)

// List-valued fields are marked true; other known fields are scalars.
var fields = map[string]bool{
	"title":          false,
	"subtitle":       false,
	"original_title": false,
	"authors":        true,
	"translators":    true,
	"series":         false,
	"series_index":   false,
	"publisher":      false,
	"pubdate":        false,
	"edition":        false,
	"collection":     false,
	"isbn":           false,
	"identifiers":    false,
	"catalog_url":    false,
	"rating":         false,
	"languages":      true,
	"tags":           true,
	"comments":       false,
	"rights":         false,
	"cover_url":      false,
}

// Field reports whether a metadata field is known and holds a list.
func Field(name string) (list, known bool) {
	list, known = fields[name]
	return
}

// Equal normalizes JSON list representations and treats unset and empty values alike.
func Equal(a, b any) bool {
	normalize := func(value any) string {
		switch v := value.(type) {
		case nil:
			return ""
		case string:
			if strings.TrimSpace(v) == "" {
				return ""
			}
			return "scalar:" + strings.TrimSpace(v)
		case []string:
			if len(v) == 0 {
				return ""
			}
			raw, _ := json.Marshal(v)
			return "json:" + string(raw)
		case []any:
			if len(v) == 0 {
				return ""
			}
			raw, _ := json.Marshal(v)
			return "json:" + string(raw)
		default:
			raw, _ := json.Marshal(v)
			return "json:" + string(raw)
		}
	}
	return normalize(a) == normalize(b)
}

// Changes stores only edits, so unrelated metadata remains intact after a save.
func Changes(current, working map[string]any) map[string]any {
	changes := make(map[string]any)
	for key, value := range working {
		if _, ok := fields[key]; ok && !Equal(current[key], value) {
			changes[key] = value
		}
	}
	return changes
}
