package metadata

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/beevik/etree"
)

// epubPackage resolves the OPF package through the EPUB container document.
func epubPackage(files []*zip.File) (string, *etree.Element, error) {
	byName := map[string]*zip.File{}
	for _, f := range files {
		byName[f.Name] = f
	}

	container := byName["META-INF/container.xml"]
	if container == nil {
		return "", nil, fmt.Errorf("EPUB is missing META-INF/container.xml")
	}

	doc, err := readZipXML(container)
	if err != nil {
		return "", nil, err
	}

	rootfile := doc.FindElement(".//rootfile")
	if rootfile == nil {
		return "", nil, fmt.Errorf("EPUB container has no rootfile")
	}

	opf := path.Clean(rootfile.SelectAttrValue("full-path", ""))
	file := byName[opf]
	if file == nil {
		return "", nil, fmt.Errorf("EPUB package file %q is missing", opf)
	}

	doc, err = readZipXML(file)
	if err != nil {
		return "", nil, err
	}

	if doc.Root() == nil {
		return "", nil, fmt.Errorf("EPUB package has no root element")
	}

	return opf, doc.Root(), nil
}

// readZipXML limits decompressed XML before parsing archive metadata.
func readZipXML(f *zip.File) (*etree.Document, error) {
	r, e := f.Open()
	if e != nil {
		return nil, e
	}

	defer func() { _ = r.Close() }()
	d := etree.NewDocument()
	raw, err := io.ReadAll(io.LimitReader(r, (8<<20)+1))
	if err != nil {
		return nil, err
	}

	if len(raw) > 8<<20 {
		return nil, fmt.Errorf("archive XML exceeds 8 MiB")
	}

	err = d.ReadFromBytes(raw)
	return d, err
}

func dcValues(meta *etree.Element, tag string) []string {
	var out []string
	for _, node := range meta.ChildElements() {
		if node.Tag == tag && strings.TrimSpace(node.Text()) != "" {
			out = append(out, strings.TrimSpace(node.Text()))
		}
	}
	return out
}

func firstDC(meta *etree.Element, tag string) string {
	values := dcValues(meta, tag)
	if len(values) > 0 {
		return values[0]
	}
	return ""
}

func replaceDC(meta *etree.Element, tag string, values []string) {
	for _, node := range meta.ChildElements() {
		if node.Tag == strings.TrimPrefix(tag, "dc:") {
			meta.RemoveChild(node)
		}
	}
	for _, v := range values {
		if v != "" {
			meta.CreateElement(tag).SetText(v)
		}
	}
}

func setDC(meta *etree.Element, tag, value string) {
	local := strings.TrimPrefix(tag, "dc:")
	nodes := meta.FindElements("./" + local)
	if value == "" {
		for _, n := range nodes {
			meta.RemoveChild(n)
		}
		return
	}

	if len(nodes) == 0 {
		nodes = []*etree.Element{meta.CreateElement(tag)}
	}

	nodes[0].SetText(value)
	for _, n := range nodes[1:] {
		meta.RemoveChild(n)
	}
}

func upsertMeta(meta *etree.Element, name, value string) {
	var node *etree.Element
	for _, m := range meta.FindElements("./meta") {
		if m.SelectAttrValue("name", "") == name {
			node = m
			break
		}
	}

	if node == nil {
		node = meta.CreateElement("meta")
		node.CreateAttr("name", name)
	}

	setAttr(node, "content", value)
}
