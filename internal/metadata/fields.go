package metadata

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"shelfmark/internal/library"

	"golang.org/x/text/language"
)

var comicRatingPattern = regexp.MustCompile(`^\+?[0-9]+(?:\.[0-9]{1,2})?$`)

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
		if key == "pubdate" && strings.EqualFold(filepath.Ext(path), ".pdf") {
			return fmt.Errorf("publication date writing is not supported for PDF")
		}
		if strings.EqualFold(filepath.Ext(path), ".cbz") {
			switch key {
			case "title", "series", "series_index", "publisher", "pubdate", "authors", "comments", "languages", "tags", "catalog_url", "rating", "edition", "collection":
			default:
				return fmt.Errorf("%s metadata is not supported for CBZ", key)
			}
			if key == "pubdate" {
				if _, err := comicInfoDateFields(Text(value)); err != nil {
					return err
				}
			}
			if key == "rating" {
				rating := Text(value)
				number, err := strconv.ParseFloat(rating, 64)
				if err != nil || !comicRatingPattern.MatchString(rating) || number < 0 || number > 5 {
					return fmt.Errorf("ComicInfo rating must be a decimal between 0 and 5 with at most two fractional digits")
				}
			}
			if key == "languages" {
				values := Strings(value)
				if len(values) > 1 {
					return fmt.Errorf("ComicInfo supports one language tag")
				}
				if len(values) == 1 {
					if _, err := language.Parse(values[0]); err != nil {
						return fmt.Errorf("invalid ComicInfo language tag %q", values[0])
					}
				}
			}
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
