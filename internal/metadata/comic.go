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
	"strconv"
	"strings"
	"time"

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
			data, e := readComicInfo(file)
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
			data, e := readComicInfo(file)
			if e != nil {
				return e
			}
			doc := etree.NewDocument()
			e = doc.ReadFromBytes(data)
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

	// Preserve the element sequence defined by the ComicInfo v2.0 schema.
	for _, field := range []struct{ key, tag string }{
		{"title", "Title"},
		{"series", "Series"},
		{"series_index", "Number"},
		{"comments", "Summary"},
		{"authors", "Writer"},
		{"publisher", "Publisher"},
		{"tags", "Genre"},
		{"catalog_url", "Web"},
		{"languages", "LanguageISO"},
		{"edition", "Format"},
		{"collection", "SeriesGroup"},
		{"rating", "CommunityRating"},
	} {
		key, tag := field.key, field.tag
		value, ok := fields[key]
		if !ok {
			continue
		}

		setComicInfoElement(info, tag, Text(value))
	}
	if value, ok := fields["pubdate"]; ok {
		dateFields, err := comicInfoDateFields(Text(value))
		if err != nil {
			return err
		}
		for _, tag := range []string{"Year", "Month", "Day"} {
			if field, present := dateFields[tag]; present {
				setComicInfoElement(info, tag, field)
			} else {
				removeChildren(info, tag)
			}
		}
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

const maxComicInfoSize = 8 << 20

func readComicInfo(file *zip.File) ([]byte, error) {
	r, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()

	data, err := io.ReadAll(io.LimitReader(r, maxComicInfoSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxComicInfoSize {
		return nil, fmt.Errorf("ComicInfo.xml exceeds 8 MiB")
	}
	return data, nil
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
		{"Series", "series"},
		{"Number", "series_index"},
		{"Summary", "comments"},
		{"Writer", "authors"},
		{"Publisher", "publisher"},
		{"Genre", "tags"},
		{"Web", "catalog_url"},
		{"LanguageISO", "language"},
		{"Format", "edition"},
		{"SeriesGroup", "collection"},
		{"CommunityRating", "rating"},
	} {
		if value := get(entry[0]); value != "" {
			if entry[1] == "language" {
				meta["languages"] = []string{value}
			} else {
				meta[entry[1]] = value
			}
		}
	}
	if date := comicInfoDate(root); date != "" {
		meta["pubdate"] = date
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

var comicInfoElementOrder = map[string]int{
	"Title": 0, "Series": 1, "Number": 2, "Count": 3, "Volume": 4,
	"AlternateSeries": 5, "AlternateNumber": 6, "AlternateCount": 7,
	"Summary": 8, "Notes": 9, "Year": 10, "Month": 11, "Day": 12,
	"Writer": 13, "Penciller": 14, "Inker": 15, "Colorist": 16,
	"Letterer": 17, "CoverArtist": 18, "Editor": 19, "Publisher": 20,
	"Imprint": 21, "Genre": 22, "Web": 23, "PageCount": 24,
	"LanguageISO": 25, "Format": 26, "BlackAndWhite": 27, "Manga": 28,
	"Characters": 29, "Teams": 30, "Locations": 31, "ScanInformation": 32,
	"StoryArc": 33, "SeriesGroup": 34, "AgeRating": 35, "Pages": 36,
	"CommunityRating": 37, "MainCharacterOrTeam": 38, "Review": 39,
}

func setComicInfoElement(parent *etree.Element, tag, value string) {
	if node := parent.FindElement("./" + tag); node != nil {
		node.SetText(value)
		return
	}
	node := etree.NewElement(tag)
	node.SetText(value)
	order, known := comicInfoElementOrder[tag]
	if known {
		for _, child := range parent.ChildElements() {
			childOrder, childKnown := comicInfoElementOrder[child.Tag]
			if childKnown && childOrder > order {
				parent.InsertChildAt(child.Index(), node)
				return
			}
		}
	}
	parent.AddChild(node)
}

func comicInfoDateFields(value string) (map[string]string, error) {
	if value == "" {
		return map[string]string{}, nil
	}
	for _, layout := range []string{"2006", "2006-01", "2006-01-02"} {
		parsed, err := time.Parse(layout, value)
		if err != nil || parsed.Year() < 1 {
			continue
		}
		fields := map[string]string{"Year": strconv.Itoa(parsed.Year())}
		if len(value) >= len("2006-01") {
			fields["Month"] = strconv.Itoa(int(parsed.Month()))
		}
		if len(value) == len("2006-01-02") {
			fields["Day"] = strconv.Itoa(parsed.Day())
		}
		return fields, nil
	}
	return nil, fmt.Errorf("CBZ publication date must be YYYY, YYYY-MM, or YYYY-MM-DD")
}

func comicInfoDate(root *etree.Element) string {
	fieldText := func(name string) string {
		if element := root.FindElement("./" + name); element != nil {
			return strings.TrimSpace(element.Text())
		}
		return ""
	}
	year, err := strconv.Atoi(fieldText("Year"))
	if err != nil || year < 1 || year > 9999 {
		return ""
	}
	date := strconv.Itoa(year)
	monthText := fieldText("Month")
	if monthText == "" {
		return date
	}
	month, err := strconv.Atoi(monthText)
	if err != nil || month < 1 || month > 12 {
		return date
	}
	date += fmt.Sprintf("-%02d", month)
	dayText := fieldText("Day")
	if dayText == "" {
		return date
	}
	day, err := strconv.Atoi(dayText)
	parsed, parseErr := time.Parse("2006-01-02", fmt.Sprintf("%04d-%02d-%02d", year, month, day))
	if err != nil || parseErr != nil || parsed.Day() != day {
		return date
	}
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
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
