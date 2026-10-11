package library

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"maps"
	"strings"
)

// Text provides one representation of metadata for forms and search terms.
func Text(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []string:
		return strings.Join(v, ", ")
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprint(v)
	}
}

// InputText encodes list values as CSV so values containing commas or quotes
// remain one metadata item when submitted from the editor.
func InputText(name string, value any) (string, error) {
	list, known := Field(name)
	if !known || !list {
		return Text(value), nil
	}
	values := Strings(value)
	if len(values) == 0 {
		return "", nil
	}
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write(values); err != nil {
		return "", err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}

// DisplayTitle falls back to the file name when metadata has no usable title.
func DisplayTitle(values map[string]any, filename string) string {
	if title := strings.TrimSpace(Text(values["title"])); title != "" {
		return title
	}
	return filename
}

// SearchQuery uses bibliographic data before considering a noisy file name.
func SearchQuery(values map[string]any, filename string) string {
	identity := IdentifySearch(values, filename)
	if len(identity.Titles) == 0 {
		return strings.TrimSpace(strings.Join(identity.Authors, " "))
	}
	return strings.TrimSpace(identity.Titles[0] + " " + strings.Join(identity.Authors, " "))
}

// Working overlays a stored draft without mutating either source map.
func Working(current, draft map[string]any) map[string]any {
	result := make(map[string]any, len(current)+len(draft))
	maps.Copy(result, current)
	maps.Copy(result, draft)
	return result
}

// ParseInputs interprets raw form values according to the metadata schema.
// Clients send text and selections; the backend owns list/scalar conversion.
func ParseInputs(values map[string]string) (map[string]any, error) {
	result := make(map[string]any, len(values))
	for name, value := range values {
		list, known := Field(name)
		if !known {
			return nil, fmt.Errorf("unknown metadata field %q", name)
		}
		if !list {
			result[name] = value
			continue
		}
		reader := csv.NewReader(strings.NewReader(strings.TrimSpace(value)))
		reader.FieldsPerRecord = -1
		reader.TrimLeadingSpace = true
		records, err := reader.ReadAll()
		if err != nil {
			return nil, fmt.Errorf("%s must be valid comma-separated text: %w", name, err)
		}
		items := make([]string, 0)
		for _, record := range records {
			for _, item := range record {
				if item = strings.TrimSpace(item); item != "" {
					items = append(items, item)
				}
			}
		}
		result[name] = items
	}
	return result, nil
}

// FieldNames defines the canonical order for metadata editing and review.
func FieldNames() []string {
	return []string{"title", "subtitle", "original_title", "authors", "translators", "series", "series_index", "publisher", "pubdate", "edition", "collection", "isbn", "identifiers", "catalog_url", "rating", "languages", "tags", "comments", "rights"}
}

// Strings normalizes native and JSON-decoded lists without splitting names.
func Strings(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if strings.TrimSpace(v) != "" {
			return []string{v}
		}
	}
	return nil
}
