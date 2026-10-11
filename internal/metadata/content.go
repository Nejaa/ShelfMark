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
	"slices"
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

// epubCover resolves the EPUB cover image through manifest metadata instead of
// guessing from filenames.
func epubCover(files []*zip.File) ([]byte, string) {
	byName := zipIndex(files)
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
		if item.SelectAttrValue("id", "") != coverID && !slices.Contains(props, "cover-image") {
			continue
		}

		name := path.Clean(path.Join(base, urlPath(item.SelectAttrValue("href", ""))))
		if data := zipBytes(byName[name]); len(data) > 0 {
			mime := item.SelectAttrValue("media-type", "")
			if !strings.HasPrefix(mime, "image/") {
				mime = coverMIME(strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), "."))
			}

			return data, mime
		}
	}

	return nil, ""
}

// zipIndex builds constant-time lookup by archive member name.
func zipIndex(files []*zip.File) map[string]*zip.File {
	byName := map[string]*zip.File{}
	for _, f := range files {
		byName[f.Name] = f
	}

	return byName
}

// isbnClues collects barcode-derived ISBN lines before any OCR text so search
// terms can match exact identifiers even if OCR later adds noisy prose.
type isbnClues struct {
	recognized []string
	seen       map[string]bool
}

// newISBNClues starts a deduplicating collector for image-derived clues.
func newISBNClues() *isbnClues {
	return &isbnClues{seen: map[string]bool{}}
}

// addBarcodes records each decoded ISBN once in a search-friendly form.
func (c *isbnClues) addBarcodes(data []byte) {
	for _, isbn := range ISBNs(decodeISBN(data)) {
		if !c.seen[isbn] {
			c.recognized = append(c.recognized, "ISBN "+isbn)
			c.seen[isbn] = true
		}
	}
}

// scanImage reads barcodes and, when requested, OCR text. It reports whether
// OCR found body text, which ends front-matter scanning.
func (c *isbnClues) scanImage(ctx context.Context, data []byte, useOCR bool) bool {
	c.addBarcodes(data)

	if !useOCR {
		return false
	}

	text := OCR(ctx, data)
	if text == "" {
		return false
	}

	c.recognized = append(c.recognized, text)

	return looksLikeBody(text)
}

// firstArchiveCover returns the first image in filename order for cover previews
// in simple image archives that do not expose explicit cover metadata.
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

	byName := zipIndex(archive.File)
	base := path.Dir(opf)
	order := epubReadingOrder(root)
	clues := newISBNClues()
	collectEPUBCoverBarcodes(root, base, byName, clues)

	var texts []string
	imageDocs := map[string]bool{}
	for docIndex, item := range order {
		if ctx.Err() != nil {
			break
		}

		page, ok := epubSpineDocument(item, docIndex, len(order), base, byName)
		if !ok {
			continue
		}

		texts = append(texts, htmlText(page.source))
		scanEPUBDocumentImages(ctx, page, byName, imageDocs, clues, ocr)
	}

	return frontBack(strings.Join(append(clues.recognized, texts...), "\n"), 200000, 50000)
}

// collectEPUBCoverBarcodes inspects the declared cover before spine traversal
// because many books place the ISBN barcode only on the cover image.
func collectEPUBCoverBarcodes(root *etree.Element, base string, byName map[string]*zip.File, clues *isbnClues) {
	for _, item := range root.FindElements("./manifest/item") {
		props := strings.Fields(item.SelectAttrValue("properties", ""))
		if !slices.Contains(props, "cover-image") {
			continue
		}

		name := path.Clean(path.Join(base, urlPath(item.SelectAttrValue("href", ""))))
		if file := byName[name]; file != nil {
			clues.addBarcodes(zipBytes(file))
		}
	}
}

// epubSpinePage holds the parsed HTML and position flags used while sampling
// the beginning and end of a book.
type epubSpinePage struct {
	source           string
	document         *html.Node
	name             string
	opening, closing bool
}

// epubSpineDocument resolves one spine entry to parsed HTML when it falls
// inside the sampled opening or closing window.
func epubSpineDocument(item *etree.Element, index, count int, base string, byName map[string]*zip.File) (epubSpinePage, bool) {
	if !strings.Contains(item.SelectAttrValue("media-type", ""), "html") || index >= 8 && index < count-4 {
		return epubSpinePage{}, false
	}

	name := path.Clean(path.Join(base, urlPath(item.SelectAttrValue("href", ""))))
	file := byName[name]
	if file == nil {
		return epubSpinePage{}, false
	}

	source := string(zipBytes(file))
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return epubSpinePage{}, false
	}

	return epubSpinePage{source: source, document: doc, name: name, opening: index < 8, closing: index >= count-4}, true
}

// scanEPUBDocumentImages samples only the images likely to appear in front or back matter.
func scanEPUBDocumentImages(ctx context.Context, page epubSpinePage, byName map[string]*zip.File, seen map[string]bool, clues *isbnClues, ocr bool) {
	for _, src := range limitEPUBImages(htmlImageSources(page.document), page.opening, page.closing) {
		imageName := epubImageName(page.name, src)
		if seen[imageName] {
			continue
		}

		seen[imageName] = true

		if imageFile := byName[imageName]; imageFile != nil && clues.scanImage(ctx, zipBytes(imageFile), page.opening && ocr) {
			break
		}
	}
}

// epubImageName resolves document-relative image paths while discarding anchors and queries.
func epubImageName(documentName, source string) string {
	source = strings.Split(source, "#")[0]
	source = strings.Split(source, "?")[0]
	return path.Clean(path.Join(path.Dir(documentName), urlPath(source)))
}

// epubReadingOrder returns manifest items in spine order, or every manifest
// item when the spine is empty.
func epubReadingOrder(root *etree.Element) []*etree.Element {
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
	return order
}

// htmlImageSources collects HTML img and SVG image references in document order.
func htmlImageSources(doc *html.Node) []string {
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
	return images
}

// limitEPUBImages keeps the first 12 images of opening sections and the last 5
// of closing sections.
func limitEPUBImages(images []string, opening, closing bool) []string {
	limit := len(images)
	if opening && limit > 12 {
		limit = 12
	}

	if closing && limit > 5 {
		return images[max(0, limit-5):]
	}

	return images[:limit]
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

	clues := newISBNClues()
	for _, i := range ordered {
		if ctx.Err() != nil {
			break
		}

		if clues.scanImage(ctx, zipBytes(images[i]), i < 20 && ocr) {
			break
		}
	}

	return frontBack(strings.Join(clues.recognized, "\n"), 200000, 50000)
}

// fb2Text flattens FB2 body elements because bibliographic clues are usually in
// the opening body content rather than in the description block alone.
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

// elementText concatenates descendant character data without adding formatting.
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

// zipBytes bounds decompressed members so optional inspection never expands an
// archive entry beyond 16 MiB of memory.
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

// imageFiles keeps archive images in natural page order for cover and OCR sampling.
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

// naturalLess sorts "10" after "2" so numbered page files stay in reading order.
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
