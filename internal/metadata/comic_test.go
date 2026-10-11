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
)

func TestReadComicInfoSizeLimit(t *testing.T) {
	for _, test := range []struct {
		name      string
		size      int
		wantError bool
	}{
		{name: "at limit", size: maxComicInfoSize},
		{name: "over limit", size: maxComicInfoSize + 1, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := comicInfoZipEntry(t, bytes.Repeat([]byte("x"), test.size))
			got, err := readComicInfo(file)
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "exceeds 8 MiB") {
					t.Fatalf("readComicInfo() error = %v, want size-limit error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("readComicInfo() error = %v", err)
			}
			if len(got) != test.size {
				t.Fatalf("readComicInfo() returned %d bytes, want %d", len(got), test.size)
			}
		})
	}
}

func TestCBZReadWritePreservesOtherArchiveMembers(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "comic.cbz")
	writeZipFixture(t, filename, map[string][]byte{
		"ComicInfo.xml": []byte(`<ComicInfo><Title>Old title</Title><Series>Earthsea</Series><Number>2</Number><Summary>A story.</Summary><Notes>Original notes</Notes><Year>2020</Year><Month>7</Month><Day>4</Day><Writer>Old author</Writer><Publisher>Publisher</Publisher><Genre>Drama, Mystery</Genre><Web>https://example.org/comic</Web><LanguageISO>en-US</LanguageISO><Format>Digital</Format><SeriesGroup>Fantasy</SeriesGroup><CommunityRating>4.5</CommunityRating></ComicInfo>`),
		"pages/001.jpg": []byte("image bytes"),
	})

	metadata, err := readCBZFile(filename)
	if err != nil {
		t.Fatalf("readCBZFile() error = %v", err)
	}
	if got := Text(metadata["title"]); got != "Old title" {
		t.Fatalf("title = %q, want Old title", got)
	}
	if got := Strings(metadata["authors"]); len(got) != 1 || got[0] != "Old author" {
		t.Fatalf("authors = %#v, want [Old author]", got)
	}
	if got := metadata["pubdate"]; got != "2020-07-04" {
		t.Fatalf("publication date = %#v, want 2020-07-04 from ComicInfo Year/Month/Day", got)
	}
	if got := Strings(metadata["languages"]); len(got) != 1 || got[0] != "en-US" {
		t.Fatalf("languages = %#v, want [en-US]", got)
	}

	if err := Write(context.Background(), filename, map[string]any{"title": "New title", "pubdate": "2024-02-29"}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	metadata, err = readCBZFile(filename)
	if err != nil {
		t.Fatalf("readCBZFile() after write error = %v", err)
	}
	if got := Text(metadata["title"]); got != "New title" {
		t.Fatalf("title after write = %q, want New title", got)
	}
	if got := metadata["pubdate"]; got != "2024-02-29" {
		t.Fatalf("publication date after write = %#v, want 2024-02-29", got)
	}
	assertComicInfoElementsInSchemaOrder(t, filename, []string{"Title", "Series", "Number", "Summary", "Notes", "Year", "Month", "Day", "Writer", "Publisher", "Genre", "Web", "LanguageISO", "Format", "SeriesGroup", "CommunityRating"})

	archive, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatalf("open rewritten archive: %v", err)
	}
	defer func() { _ = archive.Close() }()
	for _, member := range archive.File {
		if member.Name != "pages/001.jpg" {
			continue
		}
		reader, err := member.Open()
		if err != nil {
			t.Fatalf("open preserved member: %v", err)
		}
		var actual bytes.Buffer
		_, copyErr := actual.ReadFrom(reader)
		_ = reader.Close()
		if copyErr != nil {
			t.Fatalf("read preserved member: %v", copyErr)
		}
		if !bytes.Equal(actual.Bytes(), []byte("image bytes")) {
			t.Fatalf("preserved page bytes = %q", actual.Bytes())
		}
		return
	}
	t.Fatal("rewritten archive is missing pages/001.jpg")
}

func TestCBZPublicationDatePrecisionAndClearing(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "dated.cbz")
	writeZipFixture(t, filename, map[string][]byte{"ComicInfo.xml": []byte(`<ComicInfo><Title>Book</Title><Year>1999</Year><Month>12</Month><Day>31</Day></ComicInfo>`)})
	for _, test := range []struct {
		value string
		want  []string
	}{
		{value: "2025", want: []string{"Year"}},
		{value: "2025-04", want: []string{"Year", "Month"}},
		{value: "", want: nil},
	} {
		if err := Write(context.Background(), filename, map[string]any{"pubdate": test.value}); err != nil {
			t.Fatalf("Write(pubdate=%q): %v", test.value, err)
		}
		got, err := comicInfoElementValues(t, filename)
		if err != nil {
			t.Fatal(err)
		}
		var dateElements []string
		for _, field := range []string{"Year", "Month", "Day"} {
			if _, exists := got[field]; exists {
				dateElements = append(dateElements, field)
			}
		}
		if !reflect.DeepEqual(dateElements, test.want) {
			t.Errorf("date elements after pubdate %q = %v, want %v", test.value, dateElements, test.want)
		}
	}
}

func TestCBZCommunityRatingCanBeCleared(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "rated.cbz")
	writeZipFixture(t, filename, map[string][]byte{
		"ComicInfo.xml": []byte(`<ComicInfo><Title>Book</Title><CommunityRating>4.5</CommunityRating></ComicInfo>`),
	})
	if err := Write(context.Background(), filename, map[string]any{"rating": ""}); err != nil {
		t.Fatalf("Write() clearing rating: %v", err)
	}
	values, err := comicInfoElementValues(t, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := values["CommunityRating"]; exists {
		t.Fatalf("CommunityRating remains after clearing: %v", values["CommunityRating"])
	}
}

func assertComicInfoElementsInSchemaOrder(t *testing.T, filename string, want []string) {
	t.Helper()
	got, err := comicInfoElementNames(t, filename)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ComicInfo element order = %v, want schema order %v", got, want)
	}
}

func comicInfoElementValues(t *testing.T, filename string) (map[string]string, error) {
	t.Helper()
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return nil, err
	}
	defer func() { _ = archive.Close() }()
	for _, member := range archive.File {
		if !strings.EqualFold(member.Name, "ComicInfo.xml") {
			continue
		}
		reader, err := member.Open()
		if err != nil {
			return nil, err
		}
		defer func() { _ = reader.Close() }()
		values := map[string]string{}
		decoder := xml.NewDecoder(reader)
		var current string
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				return values, nil
			}
			if err != nil {
				return nil, err
			}
			switch value := token.(type) {
			case xml.StartElement:
				current = value.Name.Local
			case xml.CharData:
				if current != "" {
					values[current] = string(bytes.TrimSpace(value))
				}
			case xml.EndElement:
				current = ""
			}
		}
	}
	return nil, os.ErrNotExist
}

func comicInfoElementNames(t *testing.T, filename string) ([]string, error) {
	t.Helper()
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return nil, err
	}
	defer func() { _ = archive.Close() }()
	for _, member := range archive.File {
		if !strings.EqualFold(member.Name, "ComicInfo.xml") {
			continue
		}
		reader, err := member.Open()
		if err != nil {
			return nil, err
		}
		defer func() { _ = reader.Close() }()
		decoder := xml.NewDecoder(reader)
		var result []string
		depth := 0
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				return result, nil
			}
			if err != nil {
				return nil, err
			}
			switch value := token.(type) {
			case xml.StartElement:
				depth++
				if depth == 2 {
					result = append(result, value.Name.Local)
				}
			case xml.EndElement:
				depth--
			}
		}
	}
	return nil, os.ErrNotExist
}

func comicInfoZipEntry(t *testing.T, data []byte) *zip.File {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "metadata.zip")
	writeZipFixture(t, filename, map[string][]byte{"ComicInfo.xml": data})
	archive, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatalf("open test ZIP: %v", err)
	}
	t.Cleanup(func() { _ = archive.Close() })
	return archive.File[0]
}

func writeZipFixture(t *testing.T, filename string, members map[string][]byte) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatalf("create ZIP fixture: %v", err)
	}
	writer := zip.NewWriter(file)
	for name, data := range members {
		member, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create ZIP member %q: %v", name, err)
		}
		if _, err := member.Write(data); err != nil {
			t.Fatalf("write ZIP member %q: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close ZIP fixture: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close ZIP fixture file: %v", err)
	}
}
