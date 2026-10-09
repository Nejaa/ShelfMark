package metadata

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/beevik/etree"
)

func readCBZFile(filename string) (map[string]any, error) {
	meta := map[string]any{"title": strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)), "authors": []string{}}
	stem := meta["title"].(string)
	if match := regexp.MustCompile(`(?i)^Tome\s+(\d+)\s*[-–—]\s*(.*)$`).FindStringSubmatch(stem); len(match) > 0 {
		meta["title"] = strings.TrimSpace(match[2])
		meta["series_index"] = match[1]
		parent := regexp.MustCompile(`(?i)\s*\[(?:integral|intégrale)]\s*$`).ReplaceAllString(filepath.Base(filepath.Dir(filename)), "")
		if parent != "" {
			meta["series"] = parent
		}
	}

	archive, err := zip.OpenReader(filename)
	if err != nil {
		return nil, fmt.Errorf("CBZ archive is not a readable ZIP: %w", err)
	}

	defer func() { _ = archive.Close() }()
	for _, file := range archive.File {
		if strings.EqualFold(file.Name, "ComicInfo.xml") {
			r, e := file.Open()
			if e != nil {
				return nil, e
			}

			data, e := io.ReadAll(r)
			_ = r.Close()
			if e != nil {
				return nil, e
			}

			doc := etree.NewDocument()
			if e = doc.ReadFromBytes(data); e != nil {
				return nil, e
			}

			root := doc.Root()
			if root == nil {
				break
			}

			mapComicInfo(meta, root)
			break
		}
	}

	return meta, nil
}

func writeCBZFile(ctx context.Context, filename string, fields map[string]any) error {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return err
	}

	defer func() { _ = archive.Close() }()
	var info *etree.Element
	for _, file := range archive.File {
		if strings.EqualFold(file.Name, "ComicInfo.xml") {
			r, e := file.Open()
			if e != nil {
				return e
			}

			doc := etree.NewDocument()
			_, e = doc.ReadFrom(r)
			_ = r.Close()
			if e != nil {
				return e
			}

			info = doc.Root()
			break
		}
	}

	if info == nil {
		info = etree.NewElement("ComicInfo")
	}

	// Only selected ComicInfo elements change; preserve other comic attributes.
	mapping := map[string]string{
		"title":          "Title",
		"subtitle":       "Subtitle",
		"authors":        "Writer",
		"translators":    "Translator",
		"series":         "Series",
		"series_index":   "Number",
		"publisher":      "Publisher",
		"pubdate":        "Year",
		"comments":       "Summary",
		"languages":      "LanguageISO",
		"tags":           "Genre",
		"isbn":           "ISBN",
		"catalog_url":    "Web",
		"rating":         "CommunityRating",
		"rights":         "Rights",
		"original_title": "OriginalTitle",
		"edition":        "Format",
		"collection":     "SeriesGroup",
		"identifiers":    "Notes",
	}
	for key, tag := range mapping {
		value, ok := fields[key]
		if !ok {
			continue
		}

		node := info.FindElement("./" + tag)
		if node == nil {
			node = info.CreateElement(tag)
		}

		node.SetText(Text(value))
	}

	var buf bytes.Buffer
	doc := etree.NewDocument()
	doc.SetRoot(info)
	doc.Indent(2)
	if _, err = doc.WriteTo(&buf); err != nil {
		return err
	}

	// Stream unchanged compressed images; replace or append ComicInfo once.
	out, err := createTempFile(filename)
	if err != nil {
		return err
	}

	tmp := out.Name()
	defer func() { _ = os.Remove(tmp) }()
	writer := zip.NewWriter(out)
	written := false
	for _, file := range archive.File {
		if err := ctx.Err(); err != nil {
			_ = writer.Close()
			_ = out.Close()
			return err
		}
		if strings.EqualFold(file.Name, "ComicInfo.xml") {
			if written {
				continue
			}

			written = true
			dst, e := writer.Create(file.Name)
			if e == nil {
				_, e = dst.Write(buf.Bytes())
			}

			if e != nil {
				_ = writer.Close()
				_ = out.Close()
				return e
			}

			continue
		}
		if err := copyZIPMember(writer, file); err != nil {
			_ = writer.Close()
			_ = out.Close()
			return err
		}

	}

	if !written {
		dst, e := writer.Create("ComicInfo.xml")
		if e != nil {
			_ = writer.Close()
			_ = out.Close()
			return e
		}
		if _, e = dst.Write(buf.Bytes()); e != nil {
			_ = writer.Close()
			_ = out.Close()
			return e
		}
	}

	if err = writer.Close(); err != nil {
		_ = out.Close()
		return err
	}

	if err = out.Close(); err != nil {
		return err
	}

	// Windows requires the input archive handle to close before replacement.
	if err := archive.Close(); err != nil {
		return err
	}

	return replaceFile(tmp, filename)
}

func mapComicInfo(meta map[string]any, root *etree.Element) {
	get := func(tag string) string {
		e := root.FindElement("./" + tag)
		if e == nil {
			return ""
		}
		return strings.TrimSpace(e.Text())
	}
	for _, entry := range [][2]string{
		{"Title", "title"},
		{"Subtitle", "subtitle"},
		{"Series", "series"},
		{"Number", "series_index"},
		{"Publisher", "publisher"},
		{"Year", "pubdate"},
		{"Summary", "comments"},
		{"LanguageISO", "language"},
		{"ISBN", "isbn"},
		{"Web", "catalog_url"},
		{"CommunityRating", "rating"},
		{"Rights", "rights"},
		{"OriginalTitle", "original_title"},
		{"Format", "edition"},
		{"SeriesGroup", "collection"},
		{"Notes", "identifiers"},
	} {
		if value := get(entry[0]); value != "" {
			if entry[1] == "language" {
				meta["languages"] = []string{value}
			} else {
				meta[entry[1]] = value
			}
		}
	}
	for _, k := range [][2]string{{"Writer", "authors"}, {"Translator", "translators"}, {"Genre", "tags"}} {
		if v := get(k[0]); v != "" {
			parts := strings.Split(v, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			meta[k[1]] = parts
		}
	}
}

func setElement(parent *etree.Element, tag, value string) {
	node := parent.FindElement("./" + tag)
	if node == nil {
		node = parent.CreateElement(tag)
	}
	node.SetText(value)
}

func removeChildren(parent *etree.Element, tag string) {
	for _, n := range parent.FindElements("./" + tag) {
		parent.RemoveChild(n)
	}
}
