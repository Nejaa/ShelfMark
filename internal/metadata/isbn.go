package metadata

import (
	"bytes"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"regexp"
	"strings"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/oned"
)

var (
	isbnPattern = regexp.MustCompile(`(?i)(?:ISBN(?:-1[03])?\s*[:#]?\s*)?((?:97[89][\s-]?)?\d[\dXx\s-]{8,19}[\dXx])`)
	cleanISBN   = regexp.MustCompile(`[^0-9Xx]`)
)

// ISBNs extracts distinct ISBN-10 and ISBN-13 values in encounter order.
// Separators are stripped, and check digits are verified to reject unrelated
// numbers. X is accepted only as the final ISBN-10 check digit.
func ISBNs(value string) []string {
	seen := map[string]bool{}
	var out []string
	for _, match := range isbnPattern.FindAllStringSubmatch(value, -1) {
		candidate := strings.ToUpper(cleanISBN.ReplaceAllString(match[1], ""))
		if strings.Contains(candidate[:len(candidate)-1], "X") {
			continue
		}

		if validISBN(candidate) && !seen[candidate] {
			seen[candidate] = true
			out = append(out, candidate)
		}
	}

	return out
}

// validISBN dispatches to the checksum used by the candidate length.
func validISBN(candidate string) bool {
	switch len(candidate) {
	case 10:
		return validISBN10(candidate)
	case 13:
		return validISBN13(candidate)
	}

	return false
}

// validISBN10 verifies the weighted mod-11 checksum, including a terminal X.
func validISBN10(candidate string) bool {
	sum := 0
	for i, r := range candidate {
		n := int(r - '0')
		if r == 'X' {
			if i != 9 {
				continue
			}

			n = 10
		}

		sum += (10 - i) * n
	}

	return sum%11 == 0
}

// validISBN13 verifies the alternating 1/3 weighted checksum used by EAN-13 ISBNs.
func validISBN13(candidate string) bool {
	if !strings.HasPrefix(candidate, "978") && !strings.HasPrefix(candidate, "979") {
		return false
	}

	sum := 0
	for i, r := range candidate[:12] {
		n := int(r - '0')
		if i%2 == 1 {
			n *= 3
		}

		sum += n
	}

	return (10-sum%10)%10 == int(candidate[12]-'0')
}

// decodeISBN reads an EAN-13 barcode after bounding image size and pixel count.
func decodeISBN(data []byte) string {
	if len(data) == 0 || len(data) > 16<<20 {
		return ""
	}

	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return ""
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return ""
	}

	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return ""
	}

	result, err := oned.NewEAN13Reader().Decode(bitmap, nil)
	if err != nil {
		return ""
	}

	values := ISBNs(result.GetText())
	if len(values) > 0 {
		return values[0]
	}

	return ""
}
