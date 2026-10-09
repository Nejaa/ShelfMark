package metadata

import (
	"net/url"
	"strings"
)

func splitAuthors(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, " & ")
}

func fallback(value, other string) string {
	if value != "" {
		return value
	}
	return other
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func urlPath(value string) string {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return value
	}
	return decoded
}

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
