package parsed

import (
	"errors"
	"fmt"
	"io"

	"github.com/gnames/gnfmt"
)

// errNilWriter is returned when streaming output is requested against
// a nil io.Writer.
var errNilWriter = errors.New(
	"parsed: cannot write results to a nil io.Writer",
)

// WriteResults streams the serialized representation of results to w,
// writing every record separately instead of assembling the complete
// output into one large string. The format f must be one of CSV, TSV,
// CompactJSON or PrettyJSON.
//
// For CSV and TSV the header row is written exactly once, followed by
// one row per result in the order of the input slice. The withDetails
// flag controls the set of header columns. For JSON formats every
// result is written as its own JSON object followed by a newline; when
// results are empty nothing is written, so an empty result set produces
// an empty stream instead of an empty JSON array. The flatten flag
// affects only JSON output.
//
// WriteResults stops immediately and returns an error when w is nil,
// when the format is not supported, when the underlying writer fails,
// or when a write is short. Callers can detect short writes with
// errors.Is(err, io.ErrShortWrite).
func WriteResults(
	w io.Writer,
	f gnfmt.Format,
	flatten bool,
	withDetails bool,
	results []Parsed,
) error {
	if w == nil {
		return errNilWriter
	}

	switch f {
	case gnfmt.CSV, gnfmt.TSV:
		return writeDelimitedResults(w, f, withDetails, results)
	case gnfmt.CompactJSON, gnfmt.PrettyJSON:
		return writeJSONResults(w, f, flatten, results)
	default:
		return fmt.Errorf(
			"parsed: invalid output format %d (%q), supported formats are CSV, TSV, CompactJSON and PrettyJSON",
			int(f), f.String(),
		)
	}
}

// writeDelimitedResults writes a single CSV/TSV header row followed by
// one row per parsed result. Rows are produced by the same code path as
// Parsed.Output, so the bytes stay identical to per-record output.
// CSV/TSV always use the flattened representation, therefore the
// flatten setting does not apply here.
func writeDelimitedResults(
	w io.Writer,
	f gnfmt.Format,
	withDetails bool,
	results []Parsed,
) error {
	if err := writeLine(w, HeaderCSVFlat(f, withDetails)); err != nil {
		return err
	}
	for i := range results {
		if err := writeLine(w, results[i].Output(f, false)); err != nil {
			return err
		}
	}
	return nil
}

// writeJSONResults writes each result as an independent JSON object
// followed by a newline. Nothing is written for an empty slice.
func writeJSONResults(
	w io.Writer,
	f gnfmt.Format,
	flatten bool,
	results []Parsed,
) error {
	for i := range results {
		if err := writeLine(w, results[i].Output(f, flatten)); err != nil {
			return err
		}
	}
	return nil
}

// writeLine writes s and a trailing newline in a single Write call.
// It surfaces a short write as io.ErrShortWrite instead of silently
// dropping bytes. Only the bytes of a single record are held in memory
// at a time, so the peak memory does not depend on the result set size.
func writeLine(w io.Writer, s string) error {
	b := make([]byte, 0, len(s)+1)
	b = append(b, s...)
	b = append(b, '\n')

	n, err := w.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	return nil
}
