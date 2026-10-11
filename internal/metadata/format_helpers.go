package metadata

import (
	"net/url"
	"strings"
)

// splitAuthors matches the separator used when PDF metadata stores multiple names in one field.
func splitAuthors(value string) []string {
	if value == "" {
		return nil
	}

	return strings.Split(value, " & ")
}

// fallback returns the first non-empty metadata value.
func fallback(value, other string) string {
	if value != "" {
		return value
	}

	return other
}

// contains reports whether a small string slice already has a value.
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}

	return false
}

// urlPath decodes archive href values without failing on already-literal paths.
func urlPath(value string) string {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return value
	}

	return decoded
}

// coverMIME infers a data-URL media type from an image extension.
func coverMIME(ext string) string {
	switch ext {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}
