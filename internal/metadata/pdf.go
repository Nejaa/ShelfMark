package metadata

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func readPDFFile(filename string) (map[string]any, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()
	values, err := api.Properties(context.Background(), f, model.NewDefaultConfiguration())
	if err != nil {
		return nil, fmt.Errorf("read PDF metadata: %w", err)
	}

	authors := values["Author"]
	return map[string]any{
		"title":          fallback(values["Title"], strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))),
		"subtitle":       values["Subtitle"],
		"authors":        splitAuthors(authors),
		"publisher":      values["Publisher"],
		"pubdate":        values["CreationDate"],
		"comments":       values["Subject"],
		"isbn":           values["ISBN"],
		"series":         values["Series"],
		"series_index":   values["SeriesIndex"],
		"original_title": values["OriginalTitle"],
		"translators":    splitAuthors(values["Translator"]),
		"edition":        values["Edition"],
		"collection":     values["Collection"],
		"rights":         values["Rights"],
		"rating":         values["Rating"],
		"tags":           strings.Split(values["Keywords"], ", "),
		"languages":      Strings(values["Language"]),
		"identifiers":    values["Identifiers"],
		"catalog_url":    values["CatalogURL"],
	}, nil
}

func writePDFFile(ctx context.Context, filename string, fields map[string]any) error {
	properties := map[string]string{}
	for key, pdfKey := range map[string]string{
		"title":          "Title",
		"subtitle":       "Subtitle",
		"authors":        "Author",
		"publisher":      "Publisher",
		"pubdate":        "CreationDate",
		"comments":       "Subject",
		"isbn":           "ISBN",
		"series":         "Series",
		"series_index":   "SeriesIndex",
		"original_title": "OriginalTitle",
		"translators":    "Translator",
		"edition":        "Edition",
		"collection":     "Collection",
		"rights":         "Rights",
		"rating":         "Rating",
		"identifiers":    "Identifiers",
		"catalog_url":    "CatalogURL",
		"tags":           "Keywords",
		"languages":      "Language",
	} {
		if value, ok := fields[key]; ok {
			properties[pdfKey] = Text(value)
		}
	}

	tmp, err := createTempPath(filename)
	if err != nil {
		return err
	}

	defer func() { _ = os.Remove(tmp) }()
	if err := api.AddPropertiesFile(ctx, filename, tmp, properties, model.NewDefaultConfiguration()); err != nil {
		return err
	}

	return replaceFile(tmp, filename)
}
