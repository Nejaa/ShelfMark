package metadata

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/beevik/etree"
)

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
		"title":       textAt(info, "book-title"),
		"authors":     fb2Names(info.FindElements("./author")),
		"translators": fb2Names(info.FindElements("./translator")),
		"tags":        texts(info.FindElements("./genre")),
		"languages":   []string{textAt(info, "lang")},
	}
	src := root.FindElement(".//description/src-title-info/book-title")
	if src != nil {
		out["original_title"] = src.Text()
	}

	pub := root.FindElement(".//description/publish-info")
	if pub != nil {
		for _, pair := range [][2]string{{"publisher-name", "publisher"}, {"year", "pubdate"}, {"isbn", "isbn"}} {
			if v := textAt(pub, pair[0]); v != "" {
				out[pair[1]] = v
			}
		}
	}

	if seq := info.FindElement("./sequence"); seq != nil {
		out["series"] = seq.SelectAttrValue("name", "")
		out["series_index"] = seq.SelectAttrValue("number", "")
	}

	for _, node := range root.FindElements(".//description/custom-info") {
		name := strings.TrimPrefix(node.SelectAttrValue("info-type", ""), "shelfmark-")
		if name != "" {
			out[name] = node.Text()
		}
	}

	return out, nil
}

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

	// Patch standard title-info fields without rebuilding the book body.
	if _, ok := fields["title"]; ok {
		title := Text(fields["title"])
		setElement(info, "book-title", title)
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

	if v, ok := fields["original_title"]; ok {
		description := root.FindElement(".//description")
		src := description.FindElement("./src-title-info")
		if src == nil {
			src = description.CreateElement("src-title-info")
		}

		setElement(src, "book-title", Text(v))
	}

	description := root.FindElement(".//description")
	pub := description.FindElement("./publish-info")
	if pub == nil {
		pub = description.CreateElement("publish-info")
	}

	for key, tag := range map[string]string{"publisher": "publisher-name", "pubdate": "year", "isbn": "isbn"} {
		if v, ok := fields[key]; ok {
			setElement(pub, tag, Text(v))
		}
	}

	if _, series := fields["series"]; series || fields["series_index"] != nil {
		v := fields["series"]
		seq := info.FindElement("./sequence")
		if seq == nil {
			seq = info.CreateElement("sequence")
		}

		if v != nil {
			setAttr(seq, "name", Text(v))
		}

		if idx, ok := fields["series_index"]; ok {
			setAttr(seq, "number", Text(idx))
		}
	}

	// Fields without an FB2 element use Shelfmark-prefixed custom-info entries.
	for _, key := range []string{"rating", "identifiers", "catalog_url", "subtitle", "edition", "collection", "rights"} {
		if v, ok := fields[key]; ok {
			node := findCustom(description, key)
			if node == nil {
				node = description.CreateElement("custom-info")
				node.CreateAttr("info-type", "shelfmark-"+key)
			}
			node.SetText(Text(v))
		}
	}

	return writeXMLAtomic(filename, doc)
}

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

func texts(nodes []*etree.Element) []string {
	var out []string
	for _, n := range nodes {
		if s := strings.TrimSpace(n.Text()); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func textAt(node *etree.Element, path string) string {
	found := node.FindElement("./" + path)
	if found == nil {
		return ""
	}
	return strings.TrimSpace(found.Text())
}

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

func setAttr(node *etree.Element, key, value string) {
	if attr := node.SelectAttr(key); attr != nil {
		attr.Value = value
		return
	}
	node.CreateAttr(key, value)
}

func findCustom(parent *etree.Element, key string) *etree.Element {
	for _, n := range parent.FindElements("./custom-info") {
		if n.SelectAttrValue("info-type", "") == "shelfmark-"+key {
			return n
		}
	}
	return nil
}

func elementTextOrEmpty(node *etree.Element) string {
	if node == nil {
		return ""
	}
	return elementText(node)
}
