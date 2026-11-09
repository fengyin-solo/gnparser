package parsed_test

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/gnames/gnfmt"
	"github.com/gnames/gnparser/ent/parsed"
	"github.com/stretchr/testify/assert"
)

// errBoom is a sentinel error emulating an underlying writer failure.
var errBoom = errors.New("boom: simulated writer failure")

// shortWriter accepts every write but never writes all bytes, without
// reporting an error. It emulates a misbehaving io.Writer.
type shortWriter struct {
	calls int
}

func (w *shortWriter) Write(p []byte) (int, error) {
	w.calls++
	return len(p) / 2, nil
}

// failWriter works until the failAt-th call, then returns errBoom.
// Successful writes are buffered so partial output can be inspected.
type failWriter struct {
	err    error
	failAt int
	calls  int
	buf    bytes.Buffer
}

func (w *failWriter) Write(p []byte) (int, error) {
	idx := w.calls
	w.calls++
	if idx == w.failAt {
		return 0, w.err
	}
	return w.buf.Write(p)
}

// countWriter counts individual Write calls to prove that output is
// streamed record by record instead of merged into one big string.
type countWriter struct {
	calls   int
	maxSize int
	buf     bytes.Buffer
}

func (w *countWriter) Write(p []byte) (int, error) {
	w.calls++
	if len(p) > w.maxSize {
		w.maxSize = len(p)
	}
	return w.buf.Write(p)
}

func sampleResults() []parsed.Parsed {
	return []parsed.Parsed{
		{
			Parsed:        true,
			Verbatim:      "Homo sapiens Linnaeus, 1758",
			Cardinality:   2,
			ParseQuality:  1,
			VerbatimID:    "id-1",
			ParserVersion: "v1.0.0",
			Canonical: &parsed.Canonical{
				Stemmed: "Homo sapien",
				Simple:  "Homo sapiens",
				Full:    "Homo sapiens",
			},
			Authorship: &parsed.Authorship{
				Verbatim: "Linnaeus, 1758",
				Year:     "1758",
				Authors:  []string{"Linnaeus"},
			},
		},
		{
			Parsed:       true,
			Verbatim:     "Bubo bubo",
			Cardinality:  2,
			ParseQuality: 1,
			VerbatimID:   "id-2",
			Canonical: &parsed.Canonical{
				Stemmed: "Bubo bubo",
				Simple:  "Bubo bubo",
				Full:    "Bubo bubo",
			},
		},
		{
			Parsed:       false,
			Verbatim:     "not a name",
			Cardinality:  0,
			ParseQuality: 0,
			VerbatimID:   "id-3",
		},
	}
}

// detailedResults returns samples where the first record carries
// Details, so its CSV/TSV row uses the extended 36-column layout.
func detailedResults() []parsed.Parsed {
	res := sampleResults()
	res[0].Details = parsed.DetailsSpecies{
		Species: parsed.Species{Genus: "Homo", Species: "sapiens"},
	}
	return res
}

// expectedStream reproduces the pre-streaming CLI semantics: one header
// line for CSV/TSV and one Output line per record, each ending with \n.
// It is the byte-for-byte reference that WriteResults has to match.
func expectedStream(
	f gnfmt.Format,
	flatten, withDetails bool,
	res []parsed.Parsed,
) string {
	var b strings.Builder
	if f == gnfmt.CSV || f == gnfmt.TSV {
		b.WriteString(parsed.HeaderCSV(f, withDetails))
		b.WriteByte('\n')
	}
	for i := range res {
		b.WriteString(res[i].Output(f, flatten))
		b.WriteByte('\n')
	}
	return b.String()
}

func TestWriteResults_AllFormats(t *testing.T) {
	formats := []gnfmt.Format{
		gnfmt.CSV,
		gnfmt.TSV,
		gnfmt.CompactJSON,
		gnfmt.PrettyJSON,
	}
	flattenModes := []bool{false, true}

	for _, f := range formats {
		for _, flatten := range flattenModes {
			name := fmt.Sprintf("%s/flatten=%v", f.String(), flatten)
			t.Run(name, func(t *testing.T) {
				res := sampleResults()
				var buf bytes.Buffer
				err := parsed.WriteResults(&buf, f, flatten, true, res)
				assert.Nil(t, err)
				assert.Equal(
					t,
					expectedStream(f, flatten, true, res),
					buf.String(),
				)
			})
		}
	}
}

func TestWriteResults_Single(t *testing.T) {
	for _, f := range []gnfmt.Format{
		gnfmt.CSV, gnfmt.TSV, gnfmt.CompactJSON, gnfmt.PrettyJSON,
	} {
		t.Run(f.String(), func(t *testing.T) {
			res := sampleResults()[:1]
			var buf bytes.Buffer
			err := parsed.WriteResults(&buf, f, false, false, res)
			assert.Nil(t, err)
			assert.Equal(
				t,
				expectedStream(f, false, false, res),
				buf.String(),
			)
		})
	}
}

func TestWriteResults_Order(t *testing.T) {
	res := sampleResults()
	var buf bytes.Buffer
	err := parsed.WriteResults(&buf, gnfmt.CompactJSON, false, false, res)
	assert.Nil(t, err)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	assert.Len(t, lines, len(res))
	for i, line := range lines {
		assert.Contains(t, line, fmt.Sprintf(`"id":"%s"`, res[i].VerbatimID))
	}
}

func TestWriteResults_Empty(t *testing.T) {
	t.Run("CSV empty still emits header", func(t *testing.T) {
		var buf bytes.Buffer
		err := parsed.WriteResults(&buf, gnfmt.CSV, false, false, nil)
		assert.Nil(t, err)
		assert.Equal(t, parsed.HeaderCSV(gnfmt.CSV, false)+"\n", buf.String())
	})

	t.Run("TSV empty still emits header", func(t *testing.T) {
		var buf bytes.Buffer
		err := parsed.WriteResults(&buf, gnfmt.TSV, false, true, nil)
		assert.Nil(t, err)
		assert.Equal(t, parsed.HeaderCSV(gnfmt.TSV, true)+"\n", buf.String())
	})

	for _, f := range []gnfmt.Format{gnfmt.CompactJSON, gnfmt.PrettyJSON} {
		t.Run(f.String()+" empty writes nothing", func(t *testing.T) {
			w := &countWriter{}
			err := parsed.WriteResults(w, f, false, false, nil)
			assert.Nil(t, err)
			assert.Equal(t, 0, w.calls)
			assert.Equal(t, "", w.buf.String())
		})
	}
}

func TestWriteResults_Flatten(t *testing.T) {
	res := sampleResults()[:1]

	t.Run("compact flatten true", func(t *testing.T) {
		var buf bytes.Buffer
		err := parsed.WriteResults(&buf, gnfmt.CompactJSON, true, false, res)
		assert.Nil(t, err)
		assert.Contains(t, buf.String(), `"canonicalSimple":"Homo sapiens"`)
		assert.NotContains(t, buf.String(), `"canonical":`)
	})

	t.Run("compact flatten false keeps nested object", func(t *testing.T) {
		var buf bytes.Buffer
		err := parsed.WriteResults(&buf, gnfmt.CompactJSON, false, false, res)
		assert.Nil(t, err)
		assert.Contains(t, buf.String(), `"canonical":`)
		assert.NotContains(t, buf.String(), `"canonicalSimple"`)
	})

	t.Run("pretty flatten true", func(t *testing.T) {
		var buf bytes.Buffer
		err := parsed.WriteResults(&buf, gnfmt.PrettyJSON, true, false, res)
		assert.Nil(t, err)
		assert.Contains(t, buf.String(), `"canonicalSimple": "Homo sapiens"`)
		assert.NotContains(t, buf.String(), `"canonical":`)
	})
}

func TestWriteResults_Details(t *testing.T) {
	res := detailedResults()

	t.Run("without details produces 10 columns", func(t *testing.T) {
		var buf bytes.Buffer
		err := parsed.WriteResults(&buf, gnfmt.CSV, false, false, res)
		assert.Nil(t, err)

		r := csv.NewReader(strings.NewReader(buf.String()))
		r.FieldsPerRecord = -1
		records, err := r.ReadAll()
		assert.Nil(t, err)
		// header + 3 rows
		assert.Len(t, records, 4)
		// Header follows the withDetails flag; row 0 still carries its
		// own extended columns because its Details field is populated.
		assert.Len(t, records[0], 10)
		assert.Len(t, records[1], 36)
		assert.Len(t, records[2], 10)
		assert.Len(t, records[3], 10)
	})

	t.Run("with details produces 36-column header and per-record rows", func(t *testing.T) {
		var buf bytes.Buffer
		err := parsed.WriteResults(&buf, gnfmt.TSV, false, true, res)
		assert.Nil(t, err)

		r := csv.NewReader(strings.NewReader(buf.String()))
		r.Comma = '\t'
		// Rows follow each record's own hasDetails(): records without
		// Details carry only the 10 base columns, so allow ragged rows.
		r.FieldsPerRecord = -1
		records, err := r.ReadAll()
		assert.Nil(t, err)
		assert.Len(t, records, 4)
		assert.Len(t, records[0], 36)
		assert.Len(t, records[1], 36)
		assert.Len(t, records[2], 10)
		assert.Len(t, records[3], 10)
		// header carries extended columns, the detailed row carries values.
		genusIdx := -1
		for i, name := range records[0] {
			if name == "Genus" {
				genusIdx = i
				break
			}
		}
		assert.NotEqual(t, -1, genusIdx)
		assert.Equal(t, "Homo", records[1][genusIdx])
	})
}

func TestWriteResults_NilWriter(t *testing.T) {
	for _, f := range []gnfmt.Format{
		gnfmt.CSV, gnfmt.TSV, gnfmt.CompactJSON, gnfmt.PrettyJSON,
	} {
		t.Run(f.String(), func(t *testing.T) {
			err := parsed.WriteResults(nil, f, false, false, sampleResults())
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "nil")
		})
	}
}

func TestWriteResults_InvalidFormat(t *testing.T) {
	for _, f := range []gnfmt.Format{gnfmt.FormatNone, gnfmt.Format(999)} {
		t.Run(fmt.Sprintf("format=%d", f), func(t *testing.T) {
			w := &countWriter{}
			err := parsed.WriteResults(w, f, false, false, sampleResults())
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "invalid output format")
			// Nothing must be written before the error is reported.
			assert.Equal(t, 0, w.calls)
			assert.Equal(t, "", w.buf.String())
		})
	}
}

func TestWriteResults_ShortWrite(t *testing.T) {
	for _, f := range []gnfmt.Format{
		gnfmt.CSV, gnfmt.TSV, gnfmt.CompactJSON, gnfmt.PrettyJSON,
	} {
		t.Run(f.String(), func(t *testing.T) {
			w := &shortWriter{}
			err := parsed.WriteResults(w, f, false, false, sampleResults())
			assert.Error(t, err)
			assert.True(t, errors.Is(err, io.ErrShortWrite))
			assert.Equal(t, 1, w.calls, "must stop at the first short write")
		})
	}
}

func TestWriteResults_WriterError(t *testing.T) {
	t.Run("first write fails", func(t *testing.T) {
		w := &failWriter{err: errBoom, failAt: 0}
		err := parsed.WriteResults(w, gnfmt.CSV, false, false, sampleResults())
		assert.Error(t, err)
		assert.True(t, errors.Is(err, errBoom))
		assert.Equal(t, 1, w.calls)
		assert.Equal(t, "", w.buf.String())
	})

	t.Run("fails midway through CSV and stops immediately", func(t *testing.T) {
		res := sampleResults()
		w := &failWriter{err: errBoom, failAt: 2}
		err := parsed.WriteResults(w, gnfmt.CSV, false, false, res)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, errBoom))
		// header (call 0), first row (call 1) succeed, second row fails.
		assert.Equal(t, 3, w.calls)

		lines := strings.Split(strings.TrimRight(w.buf.String(), "\n"), "\n")
		assert.Len(t, lines, 2)
		assert.Equal(t, parsed.HeaderCSV(gnfmt.CSV, false), lines[0])
		assert.Equal(t, res[0].Output(gnfmt.CSV, false), lines[1])
	})

	t.Run("fails midway through JSON and stops immediately", func(t *testing.T) {
		res := sampleResults()
		w := &failWriter{err: errBoom, failAt: 1}
		err := parsed.WriteResults(w, gnfmt.CompactJSON, false, false, res)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, errBoom))
		assert.Equal(t, 2, w.calls)

		lines := strings.Split(strings.TrimRight(w.buf.String(), "\n"), "\n")
		assert.Len(t, lines, 1)
		assert.Equal(t, res[0].Output(gnfmt.CompactJSON, false), lines[0])
	})
}

func TestWriteResults_StreamsIncrementally(t *testing.T) {
	res := sampleResults()
	w := &countWriter{}
	err := parsed.WriteResults(w, gnfmt.CSV, false, false, res)
	assert.Nil(t, err)

	// One header write + one write per record, never a single merged write.
	assert.Equal(t, 1+len(res), w.calls)

	// The biggest write must be a single line (record or header), not the
	// whole serialized result set.
	fullSize := len(expectedStream(gnfmt.CSV, false, false, res))
	assert.Less(t, w.maxSize, fullSize)

	longestLine := len(parsed.HeaderCSV(gnfmt.CSV, false)) + 1
	for i := range res {
		if size := len(res[i].Output(gnfmt.CSV, false)) + 1; size > longestLine {
			longestLine = size
		}
	}
	assert.Equal(t, longestLine, w.maxSize)
}

func TestWriteResults_ConcurrentIndependentWriters(t *testing.T) {
	formats := []gnfmt.Format{
		gnfmt.CSV, gnfmt.TSV, gnfmt.CompactJSON, gnfmt.PrettyJSON,
	}
	res := sampleResults()

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errCh := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			f := formats[i%len(formats)]
			flatten := i%2 == 0
			withDetails := i%3 == 0
			var buf bytes.Buffer
			if err := parsed.WriteResults(&buf, f, flatten, withDetails, res); err != nil {
				errCh <- err
				return
			}
			want := expectedStream(f, flatten, withDetails, res)
			if buf.String() != want {
				errCh <- fmt.Errorf("goroutine %d: output mismatch", i)
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		assert.Nil(t, err)
	}
}
