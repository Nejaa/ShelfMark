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

		name := node.SelectAttrValue("name", "")
		value := node.SelectAttrValue("content", "")
		switch name {
		case "calibre:series":
			out["series"] = value
		case "calibre:series_index":
			out["series_index"] = value
		case "calibre:subtitle":
			out["subtitle"] = value
		case "calibre:original_title":
			out["original_title"] = value
		case "calibre:edition":
			out["edition"] = value
		case "calibre:collection":
			out["collection"] = value
		case "calibre:rating":
			out["rating"] = value
		case "calibre:identifiers":
			out["identifiers"] = value
		case "calibre:catalog_url":
			out["catalog_url"] = value
		}
	}

	return out, nil
}

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

	// Locate an existing EPUB 2/3 cover or add a new manifest entry.
	coverData, coverType, coverMember := "", "", ""
	if value := Text(fields["cover_url"]); value != "" {
		coverData, coverType, err = downloadCover(ctx, value)
		if err != nil {
			return err
		}

		manifest := root.FindElement("./manifest")
		if manifest == nil {
			return fmt.Errorf("EPUB package is missing manifest")
		}

		items := manifest.FindElements("./item")
		var cover *etree.Element
		legacy := ""
		for _, m := range meta.FindElements("./meta") {
			if m.SelectAttrValue("name", "") == "cover" {
				legacy = m.SelectAttrValue("content", "")
			}
		}

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
		coverMember = path.Join(path.Dir(opf), urlPath(cover.SelectAttrValue("href", "")))
	}

	// Apply selected Dublin Core fields, preserving unrelated package metadata.
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
		replaceEPUBContributors(meta, "aut", Strings(values), strings.HasPrefix(root.SelectAttrValue("version", ""), "3"))
	}

	// Change ISBN identifiers without deleting other identifier schemes.
	if value, ok := fields["isbn"]; ok {
		ids := meta.FindElements("./identifier")
		var node *etree.Element
		for _, id := range ids {
			if len(ISBNs(id.Text())) > 0 || strings.EqualFold(id.SelectAttrValue("scheme", ""), "isbn") {
				node = id
				break
			}
		}

		if Text(value) == "" {
			for _, id := range ids {
				if len(ISBNs(id.Text())) > 0 || strings.EqualFold(id.SelectAttrValue("scheme", ""), "isbn") {
					meta.RemoveChild(id)
				}
			}
		} else {
			if node == nil {
				node = meta.CreateElement("dc:identifier")
				node.CreateAttr("opf:scheme", "ISBN")
			}
			node.SetText("ISBN:" + Text(value))
		}
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
		replaceEPUBContributors(meta, "trl", Strings(values), strings.HasPrefix(root.SelectAttrValue("version", ""), "3"))
	}

	// Copy untouched members as compressed bytes. Rewrite only the OPF and
	// selected cover, preserving archive order and the EPUB mimetype entry.
	out, err := createTempFile(filename)
	if err != nil {
		return err
	}

	tmp := out.Name()
	defer func() { _ = os.Remove(tmp) }()
	writer := zip.NewWriter(out)
	found := false
	for _, item := range archive.File {
		if err := ctx.Err(); err != nil {
			_ = writer.Close()
			_ = out.Close()
			return err
		}

		if item.Name != opf && (coverData == "" || item.Name != coverMember) {
			if err := copyZIPMember(writer, item); err != nil {
				_ = writer.Close()
				_ = out.Close()
				return err
			}
			continue
		}

		reader, e := item.Open()
		if e != nil {
			_ = writer.Close()
			_ = out.Close()
			return e
		}

		data, e := io.ReadAll(reader)
		_ = reader.Close()
		if e != nil {
			_ = writer.Close()
			_ = out.Close()
			return e
		}

		if item.Name == opf {
			doc := etree.NewDocument()
			doc.SetRoot(root)
			data, err = doc.WriteToBytes()
			if err != nil {
				_ = writer.Close()
				_ = out.Close()
				return err
			}
		}

		if coverData != "" && item.Name == coverMember {
			data, _ = base64Decode(coverData)
			found = true
		}

		header := item.FileHeader
		dst, e := writer.CreateHeader(&header)
		if e == nil {
			_, e = dst.Write(data)
		}

		if e != nil {
			_ = writer.Close()
			_ = out.Close()
			return e
		}
	}

	if coverData != "" && !found {
		data, _ := base64Decode(coverData)
		dst, err := writer.Create(coverMember)
		if err != nil {
			_ = writer.Close()
			_ = out.Close()
			return err
		}

		_, err = dst.Write(data)
		if err != nil {
			_ = writer.Close()
			_ = out.Close()
			return err
		}
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
