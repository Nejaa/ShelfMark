package metadata

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func TestEPUBMetadataRoundTripPreservesUnselectedMembers(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "book.epub")
	chapter := []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p>Chapter text.</p></body></html>`)
	writeEPUBFixture(t, filename, "Original title", chapter)

	if err := Write(context.Background(), filename, map[string]any{"title": "Updated title", "pubdate": "2024"}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got, err := Read(filename)
	if err != nil {
		t.Fatalf("Read() after write error = %v", err)
	}
	if got["title"] != "Updated title" || got["pubdate"] != "2024" {
		t.Fatalf("updated metadata = title %q, publication date %q", got["title"], got["pubdate"])
	}
	if !reflect.DeepEqual(got["authors"], []string{"Original author"}) || !reflect.DeepEqual(got["tags"], []string{"Historical fiction"}) {
		t.Fatalf("unselected metadata changed: authors=%#v tags=%#v", got["authors"], got["tags"])
	}
	if !reflect.DeepEqual(got["translators"], []string{"Jane Translator"}) {
		t.Fatalf("EPUB 2 opf:role translator = %#v, want [Jane Translator]", got["translators"])
	}
	assertEPUBPackageMetadata(t, filename)

	archive, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatalf("open rewritten EPUB: %v", err)
	}
	defer func() { _ = archive.Close() }()
	if len(archive.File) == 0 || archive.File[0].Name != "mimetype" || archive.File[0].Method != zip.Store {
		t.Fatal("EPUB output must begin with an uncompressed mimetype entry")
	}
	for _, member := range archive.File {
		if member.Name != "OEBPS/chapter.xhtml" {
			continue
		}
		reader, err := member.Open()
		if err != nil {
			t.Fatalf("open chapter: %v", err)
		}
		data, readErr := io.ReadAll(reader)
		_ = reader.Close()
		if readErr != nil {
			t.Fatalf("read chapter: %v", readErr)
		}
		if !bytes.Equal(data, chapter) {
			t.Fatalf("chapter bytes = %q, want %q", data, chapter)
		}
		return
	}
	t.Fatal("rewritten EPUB is missing its chapter")
}

func writeEPUBFixture(t *testing.T, filename, title string, chapter []byte) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	mimetype, err := writer.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(mimetype, "application/epub+zip"); err != nil {
		t.Fatal(err)
	}
	entries := []struct{ name, content string }{
		{"META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf" xmlns:roles="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="BookId"><metadata><dc:title>` + title + `</dc:title><dc:identifier id="BookId" opf:scheme="ISBN">urn:isbn:9780306406157</dc:identifier><dc:language>en</dc:language><dc:creator opf:role="aut">Original author</dc:creator><dc:contributor roles:role="trl">Jane Translator</dc:contributor><dc:subject>Historical fiction</dc:subject><dc:date opf:event="publication">1999</dc:date></metadata><manifest><item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="chapter"/></spine></package>`},
	}
	for _, entry := range entries {
		member, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(member, entry.content); err != nil {
			t.Fatal(err)
		}
	}
	member, err := writer.Create("OEBPS/chapter.xhtml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := member.Write(chapter); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertEPUBPackageMetadata(t *testing.T, filename string) {
	t.Helper()
	archive, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()
	for _, member := range archive.File {
		if member.Name != "OEBPS/content.opf" {
			continue
		}
		reader, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		var packageDoc struct {
			XMLName          xml.Name `xml:"http://www.idpf.org/2007/opf package"`
			UniqueIdentifier string   `xml:"unique-identifier,attr"`
			Metadata         struct {
				Title      string `xml:"http://purl.org/dc/elements/1.1/ title"`
				Identifier []struct {
					ID    string `xml:"id,attr"`
					Value string `xml:",chardata"`
				} `xml:"http://purl.org/dc/elements/1.1/ identifier"`
				Creator []string `xml:"http://purl.org/dc/elements/1.1/ creator"`
				Date    []struct {
					Event string `xml:"http://www.idpf.org/2007/opf event,attr"`
					Value string `xml:",chardata"`
				} `xml:"http://purl.org/dc/elements/1.1/ date"`
			} `xml:"http://www.idpf.org/2007/opf metadata"`
		}
		decodeErr := xml.NewDecoder(reader).Decode(&packageDoc)
		_ = reader.Close()
		if decodeErr != nil {
			t.Fatalf("decode OPF package: %v", decodeErr)
		}
		if packageDoc.XMLName.Space != "http://www.idpf.org/2007/opf" || packageDoc.UniqueIdentifier != "BookId" {
			t.Fatalf("OPF package identity is invalid: %+v", packageDoc)
		}
		if packageDoc.Metadata.Title != "Updated title" || len(packageDoc.Metadata.Creator) != 1 || packageDoc.Metadata.Creator[0] != "Original author" {
			t.Fatalf("OPF Dublin Core metadata = %+v", packageDoc.Metadata)
		}
		if len(packageDoc.Metadata.Identifier) != 1 || packageDoc.Metadata.Identifier[0].ID != packageDoc.UniqueIdentifier || packageDoc.Metadata.Identifier[0].Value != "urn:isbn:9780306406157" {
			t.Fatalf("OPF unique identifier is missing or changed: %+v", packageDoc.Metadata.Identifier)
		}
		if len(packageDoc.Metadata.Date) != 1 || packageDoc.Metadata.Date[0].Event != "publication" || packageDoc.Metadata.Date[0].Value != "2024" {
			t.Fatalf("OPF publication date event/value = %+v", packageDoc.Metadata.Date)
		}
		return
	}
	t.Fatal("rewritten EPUB is missing its package document")
}

func TestFB2MetadataRoundTripPreservesBookBody(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "book.fb2")
	body := `Keep this body intact.`
	original := `<?xml version="1.0" encoding="UTF-8"?><FictionBook><description><title-info><book-title>Original title</book-title><author><first-name>Old</first-name><last-name>Author</last-name></author><lang>en</lang></title-info><publish-info><publisher-name>Press</publisher-name><year>2001</year></publish-info></description><body><section><p>` + body + `</p></section></body></FictionBook>`
	if err := os.WriteFile(filename, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}

	if err := Write(context.Background(), filename, map[string]any{"title": "Updated title", "authors": []string{"New Author"}, "pubdate": "2025"}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got, err := Read(filename)
	if err != nil {
		t.Fatalf("Read() after write error = %v", err)
	}
	if got["title"] != "Updated title" || got["pubdate"] != "2025" || !reflect.DeepEqual(got["authors"], []string{"New Author"}) {
		t.Fatalf("updated metadata = %#v", got)
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), body) {
		t.Fatalf("FB2 body was not preserved: %s", data)
	}
}

func TestPDFPublicationDateWriteRejectedWithoutModifyingFile(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "book.pdf")
	var pdf bytes.Buffer
	configuration := model.NewDefaultConfiguration()
	configuration.CreationDate = "2000-01-02 03:04"
	if err := api.Create(context.Background(), nil, strings.NewReader(`{"pages":{"1":{"content":{}}}}`), &pdf, configuration); err != nil {
		t.Fatalf("create PDF fixture: %v", err)
	}
	if err := os.WriteFile(filename, pdf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	before := readPDFProperties(t, filename)
	metadata, err := Read(filename)
	if err != nil {
		t.Fatalf("Read() PDF: %v", err)
	}
	if _, exists := metadata["pubdate"]; exists {
		t.Fatalf("PDF CreationDate was incorrectly exposed as publication date: %#v", metadata["pubdate"])
	}
	if err := Write(context.Background(), filename, map[string]any{"pubdate": "2020"}); err == nil {
		t.Fatal("Write() accepted a publication date for PDF")
	}
	after := readPDFProperties(t, filename)
	if after["CreationDate"] != before["CreationDate"] {
		t.Fatalf("rejected publication-date edit changed CreationDate: before=%q after=%q", before["CreationDate"], after["CreationDate"])
	}
}

func readPDFProperties(t *testing.T, filename string) map[string]string {
	t.Helper()
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	properties, err := api.Properties(context.Background(), file, model.NewDefaultConfiguration())
	if err != nil {
		t.Fatalf("read PDF properties: %v", err)
	}
	return properties
}
