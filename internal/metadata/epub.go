package metadata

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/beevik/etree"
)

var calibreMetaFields = map[string]string{
	"calibre:series":         "series",
	"calibre:series_index":   "series_index",
	"calibre:subtitle":       "subtitle",
	"calibre:original_title": "original_title",
	"calibre:edition":        "edition",
	"calibre:collection":     "collection",
	"calibre:rating":         "rating",
	"calibre:identifiers":    "identifiers",
	"calibre:catalog_url":    "catalog_url",
}

// readEPUBFile reads Dublin Core metadata plus Calibre-specific extensions
// commonly used to store series and related ebook fields.
func readEPUBFile(filename string) (map[string]any, error) {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return nil, err
	}

	defer func() { _ = archive.Close() }()
	_, root, err := epubPackage(archive.File)
	if err != nil {
		return nil, err
	}

	meta := root.FindElement("./metadata")
	if meta == nil {
		return nil, fmt.Errorf("EPUB package is missing metadata")
	}

	authors, translators := readEPUBContributors(meta)
	out := map[string]any{
		"title":       firstDC(meta, "title"),
		"authors":     authors,
		"translators": translators,
		"publisher":   firstDC(meta, "publisher"),
		"pubdate":     firstDC(meta, "date"),
		"languages":   dcValues(meta, "language"),
		"tags":        dcValues(meta, "subject"),
		"comments":    firstDC(meta, "description"),
		"rights":      firstDC(meta, "rights"),
	}
	for _, identifier := range dcValues(meta, "identifier") {
		if values := ISBNs(identifier); len(values) > 0 {
			out["isbn"] = values[0]
			break
		}
	}

	for _, node := range meta.ChildElements() {
		if node.Tag != "meta" {
			continue
		}

		if field, ok := calibreMetaFields[node.SelectAttrValue("name", "")]; ok {
			out[field] = node.SelectAttrValue("content", "")
		}
	}

	return out, nil
}

// writeEPUBFile patches the OPF package and optional cover while leaving all
// other archive members and their ordering untouched.
func writeEPUBFile(ctx context.Context, filename string, fields map[string]any) error {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return err
	}

	defer func() { _ = archive.Close() }()

	opf, root, err := epubPackage(archive.File)
	if err != nil {
		return err
	}

	meta := root.FindElement("./metadata")
	if meta == nil {
		return fmt.Errorf("EPUB package is missing metadata")
	}

	coverData, coverType, coverMember := "", "", ""
	if value := Text(fields["cover_url"]); value != "" {
		coverData, coverType, err = downloadCover(ctx, value)
		if err != nil {
			return err
		}

		coverMember, err = prepareEPUBCover(root, meta, opf, coverType)
		if err != nil {
			return err
		}
	}

	applyEPUBFields(root, meta, fields)

	// Copy untouched members as compressed bytes. Rewrite only the OPF and
	// selected cover, preserving archive order and the EPUB mimetype entry.
	out, err := createTempFile(filename)
	if err != nil {
		return err
	}

	tmp := out.Name()
	defer func() { _ = os.Remove(tmp) }()

	writer := zip.NewWriter(out)
	if err = copyEPUBMembers(ctx, writer, archive, opf, root, coverData, coverMember); err != nil {
		_ = writer.Close()
		_ = out.Close()
		return err
	}

	if err = writer.Close(); err != nil {
		_ = out.Close()
		return err
	}

	if err = out.Close(); err != nil {
		return err
	}

	// Release the original file handle before replacing it on Windows.
	if err := archive.Close(); err != nil {
		return err
	}

	return replaceFile(tmp, filename)
}

// prepareEPUBCover locates an existing EPUB 2/3 cover or adds a manifest entry,
// and returns the archive member that must hold the new image.
func prepareEPUBCover(root, meta *etree.Element, opf, coverType string) (string, error) {
	manifest := root.FindElement("./manifest")
	if manifest == nil {
		return "", fmt.Errorf("EPUB package is missing manifest")
	}

	legacy := ""
	for _, m := range meta.FindElements("./meta") {
		if m.SelectAttrValue("name", "") == "cover" {
			legacy = m.SelectAttrValue("content", "")
		}
	}

	var cover *etree.Element
	items := manifest.FindElements("./item")
	for _, item := range items {
		props := strings.Fields(item.SelectAttrValue("properties", ""))
		if contains(props, "cover-image") || item.SelectAttrValue("id", "") == legacy {
			cover = item
			break
		}
	}

	if cover == nil {
		cover = manifest.CreateElement("item")
		cover.CreateAttr("id", "shelfmark-cover")
		cover.CreateAttr("href", "shelfmark-cover."+coverType)
		cover.CreateAttr("media-type", coverMIME(coverType))
		cover.CreateAttr("properties", "cover-image")
	}

	cover.CreateAttr("media-type", coverMIME(coverType))

	return path.Join(path.Dir(opf), urlPath(cover.SelectAttrValue("href", ""))), nil
}

// applyEPUBFields updates selected package metadata, preserving unrelated entries.
func applyEPUBFields(root, meta *etree.Element, fields map[string]any) {
	epub3 := strings.HasPrefix(root.SelectAttrValue("version", ""), "3")

	for key, tag := range map[string]string{
		"title":     "dc:title",
		"publisher": "dc:publisher",
		"pubdate":   "dc:date",
		"comments":  "dc:description",
		"rights":    "dc:rights",
	} {
		if value, ok := fields[key]; ok {
			setDC(meta, tag, Text(value))
		}
	}

	for key, tag := range map[string]string{"languages": "dc:language", "tags": "dc:subject"} {
		if values, ok := fields[key]; ok {
			replaceDC(meta, tag, Strings(values))
		}
	}

	if values, ok := fields["authors"]; ok {
		replaceEPUBContributors(meta, "aut", Strings(values), epub3)
	}

	if value, ok := fields["isbn"]; ok {
		applyEPUBISBN(meta, Text(value))
	}

	for _, key := range []string{
		"subtitle",
		"original_title",
		"edition",
		"collection",
		"rating",
		"identifiers",
		"catalog_url",
		"series",
		"series_index",
	} {
		if value, ok := fields[key]; ok {
			upsertMeta(meta, "calibre:"+key, Text(value))
		}
	}

	if values, ok := fields["translators"]; ok {
		replaceEPUBContributors(meta, "trl", Strings(values), epub3)
	}
}

// isISBNIdentifier recognizes both explicit scheme markers and raw ISBN text.
func isISBNIdentifier(id *etree.Element) bool {
	return len(ISBNs(id.Text())) > 0 || strings.EqualFold(id.SelectAttrValue("scheme", ""), "isbn")
}

// applyEPUBISBN changes ISBN identifiers without deleting other identifier schemes.
func applyEPUBISBN(meta *etree.Element, value string) {
	ids := meta.FindElements("./identifier")
	if value == "" {
		for _, id := range ids {
			if isISBNIdentifier(id) {
				meta.RemoveChild(id)
			}
		}
		return
	}

	var node *etree.Element
	for _, id := range ids {
		if isISBNIdentifier(id) {
			node = id
			break
		}
	}

	if node == nil {
		node = meta.CreateElement("dc:identifier")
		node.CreateAttr("opf:scheme", "ISBN")
	}

	node.SetText("ISBN:" + value)
}

// copyEPUBMembers streams the original archive, replacing only the OPF package
// and selected cover image so recompression stays localized.
func copyEPUBMembers(ctx context.Context, writer *zip.Writer, archive *zip.ReadCloser, opf string, root *etree.Element, coverData, coverMember string) error {
	found := false
	for _, item := range archive.File {
		if err := ctx.Err(); err != nil {
			return err
		}

		if item.Name != opf && (coverData == "" || item.Name != coverMember) {
			if err := copyZIPMember(writer, item); err != nil {
				return err
			}

			continue
		}

		isCover := coverData != "" && item.Name == coverMember
		if err := rewriteEPUBMember(writer, item, opf, root, coverData, isCover); err != nil {
			return err
		}

		found = found || isCover
	}

	if coverData != "" && !found {
		data, _ := base64Decode(coverData)
		dst, err := writer.Create(coverMember)
		if err != nil {
			return err
		}

		_, err = dst.Write(data)
		return err
	}
	return nil
}

// rewriteEPUBMember materializes one replaced member while preserving its header metadata.
func rewriteEPUBMember(writer *zip.Writer, item *zip.File, opf string, root *etree.Element, coverData string, isCover bool) error {
	reader, err := item.Open()
	if err != nil {
		return err
	}

	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		return err
	}

	if item.Name == opf {
		doc := etree.NewDocument()
		doc.SetRoot(root)
		data, err = doc.WriteToBytes()
		if err != nil {
			return err
		}
	}

	if isCover {
		data, _ = base64Decode(coverData)
	}

	header := item.FileHeader
	dst, err := writer.CreateHeader(&header)
	if err == nil {
		_, err = dst.Write(data)
	}
	return err
}
