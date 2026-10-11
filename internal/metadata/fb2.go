package metadata

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/beevik/etree"
)

const (
	fb2BookTitle    = "book-title"
	fb2CustomPrefix = "shelfmark-"
	fb2InfoType     = "info-type"
)

// readFB2File reads the standard title-info and publish-info blocks while
// retaining Shelfmark-specific values stored in custom-info elements.
func readFB2File(filename string) (map[string]any, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromFile(filename); err != nil {
		return nil, err
	}

	root := doc.Root()
	if root == nil {
		return nil, fmt.Errorf("FB2 has no root element")
	}

	info := root.FindElement(".//description/title-info")
	if info == nil {
		return map[string]any{"title": strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))}, nil
	}

	out := map[string]any{
		"comments":    elementTextOrEmpty(info.FindElement("./annotation")),
		"title":       textAt(info, fb2BookTitle),
		"authors":     fb2Names(info.FindElements("./author")),
		"translators": fb2Names(info.FindElements("./translator")),
		"tags":        texts(info.FindElements("./genre")),
		"languages":   []string{textAt(info, "lang")},
	}

	applyFB2OriginalTitle(root, out)
	applyFB2Publisher(root, out)

	if seq := info.FindElement("./sequence"); seq != nil {
		out["series"] = seq.SelectAttrValue("name", "")
		out["series_index"] = seq.SelectAttrValue("number", "")
	}

	for _, node := range root.FindElements(".//description/custom-info") {
		name := strings.TrimPrefix(node.SelectAttrValue(fb2InfoType, ""), fb2CustomPrefix)
		if name != "" {
			out[name] = node.Text()
		}
	}

	return out, nil
}

// applyFB2OriginalTitle copies the source-language title when the document provides it.
func applyFB2OriginalTitle(root *etree.Element, out map[string]any) {
	if src := root.FindElement(".//description/src-title-info/book-title"); src != nil {
		out["original_title"] = src.Text()
	}
}

// applyFB2Publisher maps publish-info fields that FB2 stores outside title-info.
func applyFB2Publisher(root *etree.Element, out map[string]any) {
	pub := root.FindElement(".//description/publish-info")
	if pub == nil {
		return
	}

	for _, pair := range [][2]string{{"publisher-name", "publisher"}, {"year", "pubdate"}, {"isbn", "isbn"}} {
		if v := textAt(pub, pair[0]); v != "" {
			out[pair[1]] = v
		}
	}
}

// writeFB2File updates selected metadata nodes in place so the rest of the FB2
// document, including the main body, stays byte-for-byte untouched where possible.
func writeFB2File(filename string, fields map[string]any) error {
	doc := etree.NewDocument()
	if err := doc.ReadFromFile(filename); err != nil {
		return err
	}

	root := doc.Root()
	if root == nil {
		return fmt.Errorf("FB2 has no root element")
	}

	info := root.FindElement(".//description/title-info")
	if info == nil {
		return fmt.Errorf("FB2 file has no title-info")
	}

	applyFB2TitleInfo(info, fields)
	description := root.FindElement(".//description")
	applyFB2OriginalTitleField(description, fields)
	applyFB2PublishInfo(description, fields)
	applyFB2Series(info, fields)

	// Fields without an FB2 element use Shelfmark-prefixed custom-info entries.
	for _, key := range []string{"rating", "identifiers", "catalog_url", "subtitle", "edition", "collection", "rights"} {
		if v, ok := fields[key]; ok {
			node := findCustom(description, key)
			if node == nil {
				node = description.CreateElement("custom-info")
				node.CreateAttr(fb2InfoType, fb2CustomPrefix+key)
			}

			node.SetText(Text(v))
		}
	}

	return writeXMLAtomic(filename, doc)
}

// applyFB2TitleInfo patches standard title-info fields without rebuilding the book body.
func applyFB2TitleInfo(info *etree.Element, fields map[string]any) {
	if v, ok := fields["title"]; ok {
		setElement(info, fb2BookTitle, Text(v))
	}

	if v, ok := fields["authors"]; ok {
		replaceFB2Names(info, "author", Strings(v))
	}

	if v, ok := fields["translators"]; ok {
		replaceFB2Names(info, "translator", Strings(v))
	}

	if v, ok := fields["tags"]; ok {
		removeChildren(info, "genre")
		for _, tag := range Strings(v) {
			info.CreateElement("genre").SetText(tag)
		}
	}

	if v, ok := fields["languages"]; ok {
		values := Strings(v)
		language := ""
		if len(values) > 0 {
			language = values[0]
		}

		setElement(info, "lang", language)
	}

	if v, ok := fields["comments"]; ok {
		removeChildren(info, "annotation")
		if Text(v) != "" {
			annotation := info.CreateElement("annotation")
			for _, line := range strings.Split(Text(v), "\n") {
				annotation.CreateElement("p").SetText(line)
			}
		}
	}
}

// applyFB2OriginalTitleField creates src-title-info on demand when setting an original title.
func applyFB2OriginalTitleField(description *etree.Element, fields map[string]any) {
	v, ok := fields["original_title"]
	if !ok {
		return
	}

	src := description.FindElement("./src-title-info")
	if src == nil {
		src = description.CreateElement("src-title-info")
	}

	setElement(src, fb2BookTitle, Text(v))
}

// applyFB2PublishInfo updates publisher, year and ISBN without disturbing other publish-info nodes.
func applyFB2PublishInfo(description *etree.Element, fields map[string]any) {
	pub := description.FindElement("./publish-info")
	if pub == nil {
		pub = description.CreateElement("publish-info")
	}

	for key, tag := range map[string]string{"publisher": "publisher-name", "pubdate": "year", "isbn": "isbn"} {
		if v, ok := fields[key]; ok {
			setElement(pub, tag, Text(v))
		}
	}
}

// applyFB2Series keeps sequence attributes in sync with the selected series fields.
func applyFB2Series(info *etree.Element, fields map[string]any) {
	if _, series := fields["series"]; !series && fields["series_index"] == nil {
		return
	}

	seq := info.FindElement("./sequence")
	if seq == nil {
		seq = info.CreateElement("sequence")
	}

	if v := fields["series"]; v != nil {
		setAttr(seq, "name", Text(v))
	}

	if idx, ok := fields["series_index"]; ok {
		setAttr(seq, "number", Text(idx))
	}
}

// fb2Names composes a display name from the split FB2 first/middle/last name elements.
func fb2Names(nodes []*etree.Element) []string {
	var out []string
	for _, n := range nodes {
		parts := []string{textAt(n, "first-name"), textAt(n, "middle-name"), textAt(n, "last-name")}
		if name := strings.TrimSpace(strings.Join(strings.Fields(strings.Join(parts, " ")), " ")); name != "" {
			out = append(out, name)
		}
	}

	return out
}

// texts returns trimmed non-empty element text values in document order.
func texts(nodes []*etree.Element) []string {
	var out []string
	for _, n := range nodes {
		if s := strings.TrimSpace(n.Text()); s != "" {
			out = append(out, s)
		}
	}

	return out
}

// textAt reads a direct child element as trimmed text.
func textAt(node *etree.Element, path string) string {
	found := node.FindElement("./" + path)
	if found == nil {
		return ""
	}

	return strings.TrimSpace(found.Text())
}

// replaceFB2Names rebuilds repeated name elements from display strings.
func replaceFB2Names(parent *etree.Element, tag string, names []string) {
	removeChildren(parent, tag)
	for _, name := range names {
		parts := strings.Fields(name)
		n := parent.CreateElement(tag)
		if len(parts) > 0 {
			n.CreateElement("first-name").SetText(parts[0])
		}

		if len(parts) > 1 {
			n.CreateElement("last-name").SetText(strings.Join(parts[1:], " "))
		}
	}
}

// setAttr updates an attribute in place or creates it when absent.
func setAttr(node *etree.Element, key, value string) {
	if attr := node.SelectAttr(key); attr != nil {
		attr.Value = value
		return
	}

	node.CreateAttr(key, value)
}

// findCustom locates Shelfmark's custom-info entry for a logical field name.
func findCustom(parent *etree.Element, key string) *etree.Element {
	for _, n := range parent.FindElements("./custom-info") {
		if n.SelectAttrValue(fb2InfoType, "") == fb2CustomPrefix+key {
			return n
		}
	}

	return nil
}

// elementTextOrEmpty keeps missing optional annotation nodes as empty strings.
func elementTextOrEmpty(node *etree.Element) string {
	if node == nil {
		return ""
	}

	return elementText(node)
}
