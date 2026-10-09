package metadata

import (
	"fmt"
	"path/filepath"
	"strings"

	"shelfmark/internal/library"
)

// ValidateFields rejects values that cannot be represented by a format writer.
func ValidateFields(path string, values map[string]any) error {
	for key, value := range values {
		list, ok := library.Field(key)
		if !ok {
			return fmt.Errorf("unknown metadata field %q", key)
		}

		if key == "cover_url" && !strings.EqualFold(filepath.Ext(path), ".epub") {
			return fmt.Errorf("cover writing requires an EPUB")
		}

		if list {
			switch v := value.(type) {
			case []string:
			case []any:
				for _, item := range v {
					if _, ok := item.(string); !ok {
						return fmt.Errorf("%s must contain strings", key)
					}
				}
			default:
				return fmt.Errorf("%s must be a list of strings", key)
			}
		} else if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be a string", key)
		}
	}
	return nil
}
