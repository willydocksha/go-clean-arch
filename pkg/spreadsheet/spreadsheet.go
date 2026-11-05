// package spreadsheet reads and writes tabular data as csv or xlsx
package spreadsheet

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// supported file formats
type Format string

const (
	CSV  Format = "csv"
	XLSX Format = "xlsx"
)

// returned for unknown formats
var ErrUnsupportedFormat = errors.New("spreadsheet: unsupported format, use csv or xlsx")

// utf-8 byte order mark (excel adds it to csv files)
const bom = "\xef\xbb\xbf"

// parse a format string ("csv", "xlsx", ".XLSX")
func ParseFormat(s string) (Format, error) {
	switch Format(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), ".")) {
	case CSV:
		return CSV, nil
	case XLSX:
		return XLSX, nil
	}
	return "", ErrUnsupportedFormat
}

// detect format from a file name
func FormatFromFilename(name string) (Format, error) {
	return ParseFormat(filepath.Ext(name))
}

// mime type for the format
func (f Format) ContentType() string {
	if f == XLSX {
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	return "text/csv; charset=utf-8"
}

// file extension including dot
func (f Format) Ext() string { return "." + string(f) }

// header row plus data rows
type Table struct {
	Headers []string
	Rows    [][]string
}

// convert rows into maps keyed by header (handy for json responses)
func (t Table) Records() []map[string]string {
	out := make([]map[string]string, 0, len(t.Rows))
	for _, row := range t.Rows {
		rec := make(map[string]string, len(t.Headers))
		for i, h := range t.Headers {
			if i < len(row) {
				rec[h] = row[i]
			} else {
				rec[h] = ""
			}
		}
		out = append(out, rec)
	}
	return out
}

// index of a header (case-insensitive), -1 if absent
func (t Table) Col(name string) int {
	for i, h := range t.Headers {
		if strings.EqualFold(h, name) {
			return i
		}
	}
	return -1
}

// ---------- write ----------

// write table in the given format
func Write(w io.Writer, f Format, t Table) error {
	switch f {
	case CSV:
		return WriteCSV(w, t)
	case XLSX:
		return WriteXLSX(w, "Sheet1", t)
	}
	return ErrUnsupportedFormat
}

// encode table into bytes
func Encode(f Format, t Table) ([]byte, error) {
	var buf bytes.Buffer
	if err := Write(&buf, f, t); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// write csv with a bom so excel opens utf-8 correctly
func WriteCSV(w io.Writer, t Table) error {
	if _, err := io.WriteString(w, bom); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(t.Headers); err != nil {
		return err
	}
	if err := cw.WriteAll(t.Rows); err != nil {
		return err
	}
	return cw.Error()
}

// write xlsx with bold header, frozen first row, autofilter and fitted widths
func WriteXLSX(w io.Writer, sheet string, t Table) error {
	f := excelize.NewFile()
	defer f.Close()

	// rename default sheet
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return err
	}

	// stream writer keeps memory low for big exports
	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return err
	}

	// header style
	style, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#E8EEF7"}},
	})
	if err != nil {
		return err
	}

	// column widths based on longest value (capped)
	widths := make([]int, len(t.Headers))
	for i, h := range t.Headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range t.Rows {
		for i := 0; i < len(row) && i < len(widths); i++ {
			widths[i] = max(widths[i], utf8.RuneCountInString(row[i]))
		}
	}
	for i, wd := range widths {
		if err := sw.SetColWidth(i+1, i+1, float64(min(wd+2, 60))); err != nil {
			return err
		}
	}

	// freeze header row
	if err := sw.SetPanes(&excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return err
	}

	// header row
	if err := sw.SetRow("A1", toCells(t.Headers, style)); err != nil {
		return err
	}

	// data rows
	for i, row := range t.Rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+2)
		if err := sw.SetRow(cell, toCells(row, 0)); err != nil {
			return err
		}
	}
	if err := sw.Flush(); err != nil {
		return err
	}

	// autofilter over the used range
	if len(t.Headers) > 0 {
		last, _ := excelize.CoordinatesToCellName(len(t.Headers), len(t.Rows)+1)
		if err := f.AutoFilter(sheet, "A1:"+last, nil); err != nil {
			return err
		}
	}
	return f.Write(w)
}

// convert strings to excel cells with optional style
func toCells(values []string, style int) []any {
	cells := make([]any, len(values))
	for i, v := range values {
		if style != 0 {
			cells[i] = excelize.Cell{StyleID: style, Value: v}
		} else {
			cells[i] = v
		}
	}
	return cells
}

// ---------- read ----------

// read a table in the given format; first non-empty row is the header
func Read(r io.Reader, f Format) (Table, error) {
	switch f {
	case CSV:
		return ReadCSV(r)
	case XLSX:
		return ReadXLSX(r, "")
	}
	return Table{}, ErrUnsupportedFormat
}

// read csv, auto-detecting comma or semicolon delimiter
func ReadCSV(r io.Reader) (Table, error) {
	br := bufio.NewReader(r)

	// strip bom if present
	if b, err := br.Peek(len(bom)); err == nil && string(b) == bom {
		_, _ = br.Discard(len(bom))
	}

	// sniff delimiter from first line
	cr := csv.NewReader(br)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	if line, err := br.Peek(4096); err == nil || errors.Is(err, io.EOF) || errors.Is(err, bufio.ErrBufferFull) {
		first, _, _ := strings.Cut(string(line), "\n")
		if strings.Count(first, ";") > strings.Count(first, ",") {
			cr.Comma = ';'
		}
	}

	rows, err := cr.ReadAll()
	if err != nil {
		return Table{}, fmt.Errorf("read csv: %w", err)
	}
	return toTable(rows), nil
}

// read xlsx; empty sheet name means the first sheet
func ReadXLSX(r io.Reader, sheet string) (Table, error) {
	f, err := excelize.OpenReader(r)
	if err != nil {
		return Table{}, fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()

	// default to first sheet
	if sheet == "" {
		sheet = f.GetSheetName(0)
	}
	rows, err := f.GetRows(sheet)
	if err != nil {
		return Table{}, fmt.Errorf("read xlsx sheet %q: %w", sheet, err)
	}
	return toTable(rows), nil
}

// split rows into header + trimmed data, skipping blank rows
func toTable(rows [][]string) Table {
	var t Table
	for _, row := range rows {
		// trim cells
		blank := true
		for i := range row {
			row[i] = strings.TrimSpace(row[i])
			if row[i] != "" {
				blank = false
			}
		}
		if blank {
			continue
		}

		// first non-blank row is the header
		if t.Headers == nil {
			t.Headers = row
			continue
		}

		// pad short rows to header width
		for len(row) < len(t.Headers) {
			row = append(row, "")
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}
