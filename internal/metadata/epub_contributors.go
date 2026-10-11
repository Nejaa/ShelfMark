package metadata

import (
	"fmt"
	"strings"

	"github.com/beevik/etree"
)

// epubContributorRoles recognizes both EPUB 2 inline roles and EPUB 3 role
// refinements. A creator without a role is an author; an untyped contributor is
// not assumed to be one (it may be an editor, producer or conversion tool).
func epubContributorRoles(meta *etree.Element) map[*etree.Element]string {
	refined := make(map[string]string)
	for _, node := range meta.ChildElements() {
		if node.Tag == "meta" && node.SelectAttrValue("property", "") == "role" {
			refined[strings.TrimPrefix(node.SelectAttrValue("refines", ""), "#")] = strings.TrimSpace(node.Text())
		}
	}

	roles := make(map[*etree.Element]string)
	for _, node := range meta.ChildElements() {
		if node.Tag != "creator" && node.Tag != "contributor" {
			continue
		}

		switch strings.ToLower(strings.TrimSpace(contributorRole(node, refined))) {
		case "aut", "author":
			roles[node] = "aut"
		case "trl", "translator":
			roles[node] = "trl"
		}
	}

	return roles
}

// contributorRole prefers an inline role, then an EPUB 3 refinement; a creator
// without either is an author.
func contributorRole(node *etree.Element, refined map[string]string) string {
	role := node.SelectAttrValue("role", "")
	if role == "" {
		for _, attr := range node.Attr {
			if attr.Key == "role" && attr.NamespaceURI() == "http://www.idpf.org/2007/opf" {
				role = attr.Value
				break
			}
		}
	}

	if role == "" {
		role = refined[node.SelectAttrValue("id", "")]
	}

	if role == "" && node.Tag == "creator" {
		role = "aut"
	}

	return role
}

// readEPUBContributors separates authors and translators using EPUB role metadata.
func readEPUBContributors(meta *etree.Element) (authors, translators []string) {
	authors, translators = []string{}, []string{}
	roles := epubContributorRoles(meta)
	for _, node := range meta.ChildElements() {
		name := strings.TrimSpace(node.Text())
		if name == "" {
			continue
		}

		switch roles[node] {
		case "aut":
			authors = append(authors, name)
		case "trl":
			translators = append(translators, name)
		}
	}

	return authors, translators
}

// replaceEPUBContributors changes only the selected role, preserving other
// contributor roles and removing refinements attached to deleted elements.
func replaceEPUBContributors(meta *etree.Element, role string, values []string, epub3 bool) {
	removeEPUBContributors(meta, role)
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}

		tag := "dc:creator"
		if role == "trl" {
			tag = "dc:contributor"
		}

		node := meta.CreateElement(tag)
		node.SetText(value)

		switch {
		case role == "aut":
		case !epub3:
			node.CreateAttr("opf:role", role)
		default:
			addEPUBRoleRefinement(meta, node, role)
		}
	}
}

// removeEPUBContributors deletes only contributors for the selected role and their refinements.
func removeEPUBContributors(meta *etree.Element, role string) {
	roles := epubContributorRoles(meta)
	for _, node := range meta.ChildElements() {
		if roles[node] != role {
			continue
		}

		if id := node.SelectAttrValue("id", ""); id != "" {
			for _, refinement := range meta.ChildElements() {
				if refinement.Tag == "meta" && refinement.SelectAttrValue("refines", "") == "#"+id {
					meta.RemoveChild(refinement)
				}
			}
		}

		meta.RemoveChild(node)
	}
}

// addEPUBRoleRefinement attaches an EPUB 3 MARC relator refinement to a contributor node.
func addEPUBRoleRefinement(meta, node *etree.Element, role string) {
	id := uniqueEPUBID(meta, role)
	node.CreateAttr("id", id)
	refinement := meta.CreateElement("meta")
	refinement.CreateAttr("refines", "#"+id)
	refinement.CreateAttr("property", "role")
	refinement.CreateAttr("scheme", "marc:relators")
	refinement.SetText(role)
}

// uniqueEPUBID avoids collisions with retained metadata from another application.
func uniqueEPUBID(meta *etree.Element, role string) string {
	for sequence := 1; ; sequence++ {
		id := fmt.Sprintf("shelfmark-%s-%d", role, sequence)

		used := false
		for _, existing := range meta.ChildElements() {
			if existing.SelectAttrValue("id", "") == id {
				used = true
				break
			}
		}

		if !used {
			return id
		}
	}
}
