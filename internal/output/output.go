package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
)

func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(s) {
	case "table", "":
		return FormatTable, nil
	case "json":
		return FormatJSON, nil
	case "yaml", "yml":
		return FormatYAML, nil
	default:
		return FormatTable, fmt.Errorf("unknown format %q (table|json|yaml)", s)
	}
}

func PrintJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func PrintYAML(v interface{}) error {
	return yaml.NewEncoder(os.Stdout).Encode(v)
}

// PrintTable prints a simple tabwriter table with a header row.
func PrintTable(headers []string, rows [][]string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	fmt.Fprintln(w, strings.Join(separators(headers), "\t"))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(pad(row, len(headers)), "\t"))
	}
	w.Flush()
}

// PrintKV prints a two-column key/value table.
func PrintKV(pairs [][2]string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, p := range pairs {
		fmt.Fprintf(w, "%s\t%s\n", p[0], p[1])
	}
	w.Flush()
}

func separators(headers []string) []string {
	s := make([]string, len(headers))
	for i, h := range headers {
		s[i] = strings.Repeat("-", len(h))
	}
	return s
}

func pad(row []string, n int) []string {
	for len(row) < n {
		row = append(row, "")
	}
	return row
}
