package metadata

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beevik/etree"
	"golang.org/x/net/html"
)

var imageExtensions = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".webp": true,
	".gif":  true,
	".tif":  true,
	".tiff": true,
	".bmp":  true,
}

func epubCover(files []*zip.File) ([]byte, string) {
	byName := map[string]*zip.File{}
	for _, f := range files {
		byName[f.Name] = f
	}

	opf, root, err := epubPackage(files)
	if err != nil {
		return nil, ""
	}

	meta := root.FindElement("./metadata")
	manifest := root.FindElement("./manifest")
	if manifest == nil || meta == nil {
		return nil, ""
	}

	coverID := ""
	for _, m := range meta.FindElements("./meta") {
		if m.SelectAttrValue("name", "") == "cover" {
			coverID = m.SelectAttrValue("content", "")
		}
	}

	base := path.Dir(opf)
	for _, item := range manifest.FindElements("./item") {
		props := strings.Fields(item.SelectAttrValue("properties", ""))
		if item.SelectAttrValue("id", "") == coverID || contains(props, "cover-image") {
			href := urlPath(item.SelectAttrValue("href", ""))
			name := path.Clean(path.Join(base, href))
			if data := zipBytes(byName[name]); len(data) > 0 {
				mime := item.SelectAttrValue("media-type", "")
				if !strings.HasPrefix(mime, "image/") {
					mime = coverMIME(strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), "."))
				}
				return data, mime
			}
		}
	}

	return nil, ""
}

func firstArchiveCover(files []*zip.File) ([]byte, string) {
	images := imageFiles(files)
	if len(images) == 0 {
		return nil, ""
	}

	name := images[0].Name
	return zipBytes(images[0]), coverMIME(strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), "."))
}

// epubText follows the spine reading order and samples opening/closing sections.
// Barcode lookup and optional OCR supplement text clues; image paths are
// deduplicated so reused cover or front-matter images are processed once.
func epubText(ctx context.Context, filename string, ocr bool) string {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		slog.Warn("archive content inspection unavailable", "path", filename, "error", err)
		return ""
	}

	defer func() { _ = archive.Close() }()
	opf, root, err := epubPackage(archive.File)
	if err != nil {
		slog.Warn("EPUB content inspection unavailable", "path", filename, "error", err)
		return ""
	}

	byName := map[string]*zip.File{}
	for _, f := range archive.File {
		byName[f.Name] = f
	}

	base := path.Dir(opf)
	manifest := map[string]*etree.Element{}
	for _, item := range root.FindElements("./manifest/item") {
		manifest[item.SelectAttrValue("id", "")] = item
	}

	var order []*etree.Element
	for _, ref := range root.FindElements("./spine/itemref") {
		if item := manifest[ref.SelectAttrValue("idref", "")]; item != nil {
			order = append(order, item)
		}
	}

	if len(order) == 0 {
		for _, item := range manifest {
			order = append(order, item)
		}
	}

	var texts, recognized []string
	isbnSeen := map[string]bool{}
	for _, item := range root.FindElements("./manifest/item") {
		props := strings.Fields(item.SelectAttrValue("properties", ""))
		if contains(props, "cover-image") {
			name := path.Clean(path.Join(base, urlPath(item.SelectAttrValue("href", ""))))
			if file := byName[name]; file != nil {
				for _, isbn := range ISBNs(decodeISBN(zipBytes(file))) {
					if !isbnSeen[isbn] {
						recognized = append(recognized, "ISBN "+isbn)
						isbnSeen[isbn] = true
					}
				}
			}
		}
	}

	imageDocs := map[string]bool{}
	for docIndex, item := range order {
		if ctx.Err() != nil {
			break
		}

		if !strings.Contains(item.SelectAttrValue("media-type", ""), "html") {
			continue
		}

		if docIndex >= 8 && docIndex < len(order)-4 {
			continue
		}

		name := path.Clean(path.Join(base, urlPath(item.SelectAttrValue("href", ""))))
		file := byName[name]
		if file == nil {
			continue
		}

		source := string(zipBytes(file))
		doc, err := html.Parse(strings.NewReader(source))
		if err != nil {
			continue
		}

		texts = append(texts, htmlText(source))
		var images []string
		var walk func(*html.Node)
		walk = func(node *html.Node) {
			if node.Type == html.ElementNode && (node.Data == "img" || node.Data == "image") {
				for _, a := range node.Attr {
					if a.Key == "src" || a.Key == "href" {
						images = append(images, a.Val)
					}
				}
			}
			for c := node.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		opening := docIndex < 8
		closing := docIndex >= len(order)-4
		limit := len(images)
		if opening && limit > 12 {
			limit = 12
		}

		if closing && limit > 5 {
			images = images[max(0, limit-5):]
			limit = len(images)
		}

		for i := 0; i < limit; i++ {
			imageName := path.Clean(path.Join(path.Dir(name), urlPath(strings.Split(strings.Split(images[i], "#")[0], "?")[0])))
			if imageDocs[imageName] {
				continue
			}

			imageDocs[imageName] = true
			if imageFile := byName[imageName]; imageFile != nil {
				data := zipBytes(imageFile)
				for _, isbn := range ISBNs(decodeISBN(data)) {
					if !isbnSeen[isbn] {
						recognized = append(recognized, "ISBN "+isbn)
						isbnSeen[isbn] = true
					}
				}
				if opening && ocr {
					if text := OCR(ctx, data); text != "" {
						recognized = append(recognized, text)
						if looksLikeBody(text) {
							opening = false
							break
						}
					}
				}
			}
		}
	}

	return frontBack(strings.Join(append(recognized, texts...), "\n"), 200000, 50000)
}

// cbzText samples the first 20 and last 10 comic images in natural filename order.
// OCR stops at likely body content; barcode lookup works without OCR enabled.
func cbzText(ctx context.Context, filename string, ocr bool) string {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		slog.Warn("archive content inspection unavailable", "path", filename, "error", err)
		return ""
	}

	defer func() { _ = archive.Close() }()
	images := imageFiles(archive.File)
	if len(images) == 0 {
		return ""
	}

	indices := map[int]bool{}
	for i := 0; i < min(20, len(images)); i++ {
		indices[i] = true
	}

	for i := max(0, len(images)-10); i < len(images); i++ {
		indices[i] = true
	}

	ordered := make([]int, 0, len(indices))
	for i := range indices {
		ordered = append(ordered, i)
	}

	sort.Ints(ordered)
	var recognized []string
	seen := map[string]bool{}
	for _, i := range ordered {
		if ctx.Err() != nil {
			break
		}

		data := zipBytes(images[i])
		for _, isbn := range ISBNs(decodeISBN(data)) {
			if !seen[isbn] {
				recognized = append(recognized, "ISBN "+isbn)
				seen[isbn] = true
			}
		}

		if i < 20 && ocr {
			if text := OCR(ctx, data); text != "" {
				recognized = append(recognized, text)
				if looksLikeBody(text) {
					break
				}
			}
		}
	}

	return frontBack(strings.Join(recognized, "\n"), 200000, 50000)
}

func fb2Text(filename string) string {
	doc := etree.NewDocument()
	if err := doc.ReadFromFile(filename); err != nil {
		slog.Warn("FB2 content inspection unavailable", "path", filename, "error", err)
		return ""
	}

	root := doc.Root()
	if root == nil {
		return ""
	}

	var parts []string
	for _, node := range root.FindElements(".//body") {
		parts = append(parts, strings.Join(strings.Fields(elementText(node)), " "))
	}

	return frontBack(strings.Join(parts, " "), 200000, 50000)
}

func elementText(node *etree.Element) string {
	var b strings.Builder
	var walk func(*etree.Element)
	walk = func(e *etree.Element) {
		for _, child := range e.Child {
			switch item := child.(type) {
			case *etree.CharData:
				b.WriteString(item.Data)
			case *etree.Element:
				walk(item)
			}
		}
	}
	walk(node)
	return b.String()
}

// zipBytes bounds decompressed members and ignores unreadable optional content.
func zipBytes(file *zip.File) []byte {
	if file == nil {
		return nil
	}

	r, err := file.Open()
	if err != nil {
		slog.Warn("archive member could not be opened", "member", file.Name, "error", err)
		return nil
	}

	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(io.LimitReader(r, (16<<20)+1))
	if err != nil || len(data) > 16<<20 {
		slog.Warn("archive member could not be inspected", "member", file.Name, "error", err, "size_limit", "16 MiB")
		return nil
	}

	return data
}

func imageFiles(files []*zip.File) []*zip.File {
	var out []*zip.File
	for _, f := range files {
		if imageExtensions[strings.ToLower(filepath.Ext(f.Name))] {
			out = append(out, f)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return naturalLess(out[i].Name, out[j].Name)
	})
	return out
}

func naturalLess(a, b string) bool {
	re := regexp.MustCompile(`\d+|\D+`)
	aa, bb := re.FindAllString(strings.ToLower(a), -1), re.FindAllString(strings.ToLower(b), -1)
	for i := 0; i < min(len(aa), len(bb)); i++ {
		x, y := aa[i], bb[i]
		xn, xe := strconv.Atoi(x)
		yn, ye := strconv.Atoi(y)
		if xe == nil && ye == nil {
			if xn != yn {
				return xn < yn
			}
			continue
		}

		if x != y {
			return x < y
		}
	}

	return len(aa) < len(bb)
}

// looksLikeBody is a heuristic stopping rule for expensive front-matter OCR.
// Chapter headings or sustained prose suggest the bibliographic pages are over.
func looksLikeBody(value string) bool {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return false
	}

	if regexp.MustCompile(`(?i)\b(chapter|chapitre)\s+(\d+|[ivxlcdm]+|one|two|three|four|five|premier|première|deux|trois|quatre|cinq)\b`).MatchString(value) {
		return true
	}

	if len(value) < 1300 || regexp.MustCompile(`(?i)\b(dedication|dédicace|acknowledg(e)?ments|remerciements|copyright|table of contents|sommaire)\b`).MatchString(value) {
		return false
	}

	return len(regexp.MustCompile(`[.!?…][”’"')\]]?\s+[A-ZÀ-Þ]`).FindAllString(value, -1)) >= 4
}

// downloadCover accepts only HTTPS, including redirects, and checks the encoded
// image size and format before it can be inserted into an EPUB.
func downloadCover(ctx context.Context, rawURL string) (string, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" {
		return "", "", fmt.Errorf("cover image URL must use HTTPS")
	}

	slog.Debug("downloading replacement cover", "host", u.Hostname())
	client := http.Client{Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many cover redirects")
		}
		if req.URL.Scheme != "https" {
			return fmt.Errorf("cover redirect must use HTTPS")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", err
	}

	req.Header.Set("User-Agent", "Shelfmark-Go/1.0")
	response, err := client.Do(req)
	if err != nil {
		return "", "", err
	}

	defer func() { _ = response.Body.Close() }()
	slog.Debug("cover download response", "host", u.Hostname(), "status", response.StatusCode)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", "", fmt.Errorf("cover download returned %s", response.Status)
	}

	limited := io.LimitReader(response.Body, 12<<20+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", "", err
	}

	if len(data) > 12<<20 {
		return "", "", fmt.Errorf("cover image is larger than 12 MB")
	}

	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", "", fmt.Errorf("unsupported cover image: %w", err)
	}

	switch format {
	case "jpeg":
		return base64.StdEncoding.EncodeToString(data), "jpg", nil
	case "png":
		return base64.StdEncoding.EncodeToString(data), "png", nil
	case "webp":
		return base64.StdEncoding.EncodeToString(data), "webp", nil
	default:
		return "", "", fmt.Errorf("unsupported cover image type %q", format)
	}
}
