package spreadsheet

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// sample table used by round-trip tests
var sample = Table{
	Headers: []string{"sku", "name", "price"},
	Rows: [][]string{
		{"PRD-1", "Kopi Susu", "18000"},
		{"PRD-2", "Teh, Manis", "8000"},
	},
}

// write then read back for every format
func TestRoundTrip(t *testing.T) {
	for _, f := range []Format{CSV, XLSX} {
		t.Run(string(f), func(t *testing.T) {
			data, err := Encode(f, sample)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Read(bytes.NewReader(data), f)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, sample) {
				t.Fatalf("round trip mismatch:\nwant %#v\ngot  %#v", sample, got)
			}
		})
	}
}

// semicolon csv (excel in id/eu locales) and blank rows
func TestReadCSVSemicolon(t *testing.T) {
	in := "sku;name\n\nPRD-1;Kopi\nPRD-2\n"
	got, err := ReadCSV(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 || got.Rows[1][1] != "" || got.Col("NAME") != 1 {
		t.Fatalf("unexpected table: %#v", got)
	}
}

// format parsing
func TestParseFormat(t *testing.T) {
	if f, _ := FormatFromFilename("Data.XLSX"); f != XLSX {
		t.Fatalf("want xlsx, got %q", f)
	}
	if _, err := ParseFormat("pdf"); err == nil {
		t.Fatal("want error for pdf")
	}
}
