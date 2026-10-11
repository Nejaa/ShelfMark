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

		if err := validateFormatSupport(path, key, value); err != nil {
			return err
		}

		if err := validateFieldShape(key, value, list); err != nil {
			return err
		}
	}

	return nil
}

// validateFormatSupport rejects fields that a specific container cannot encode.
func validateFormatSupport(path, key string, value any) error {
	extension := filepath.Ext(path)
	if key == "cover_url" && !strings.EqualFold(extension, ".epub") {
		return fmt.Errorf("cover writing requires an EPUB")
	}

	if key == "pubdate" && strings.EqualFold(extension, ".pdf") {
		return fmt.Errorf("publication date writing is not supported for PDF")
	}

	if strings.EqualFold(extension, ".cbz") {
		return validateCBZField(key, value)
	}

	return nil
}

// validateCBZField enforces the subset and value rules supported by ComicInfo.
func validateCBZField(key string, value any) error {
	switch key {
	case "title", "series", "series_index", "publisher", "pubdate", "authors", "comments", "languages", "tags", "catalog_url", "rating", "edition", "collection":
	default:
		return fmt.Errorf("%s metadata is not supported for CBZ", key)
	}
	switch key {
	case "pubdate":
		_, err := comicInfoDateFields(Text(value))
		return err
	case "rating":
		return validateComicRating(Text(value))
	case "languages":
		return validateComicLanguages(Strings(value))
	}

	return nil
}

// validateComicRating accepts an empty value, which clears the rating.
func validateComicRating(rating string) error {
	if rating == "" {
		return nil
	}

	number, err := strconv.ParseFloat(rating, 64)
	if err != nil || !comicRatingPattern.MatchString(rating) || number < 0 || number > 5 {
		return fmt.Errorf("ComicInfo rating must be a decimal between 0 and 5 with at most two fractional digits")
	}

	return nil
}

// validateComicLanguages keeps ComicInfo's single language slot parseable as BCP 47.
func validateComicLanguages(values []string) error {
	if len(values) > 1 {
		return fmt.Errorf("ComicInfo supports one language tag")
	}

	if len(values) == 1 {
		if _, err := language.Parse(values[0]); err != nil {
			return fmt.Errorf("invalid ComicInfo language tag %q", values[0])
		}
	}

	return nil
}

// validateFieldShape checks whether a value is scalar text or a list of strings.
func validateFieldShape(key string, value any, list bool) error {
	if !list {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be a string", key)
		}

		return nil
	}

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

	return nil
}
