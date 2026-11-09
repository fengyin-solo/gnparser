package gnparser_test

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/gnames/gnfmt"
	"github.com/gnames/gnparser"
	"github.com/gnames/gnparser/ent/parsed"
	"github.com/stretchr/testify/assert"
)

// streamShortWriter always performs a short write without returning an
// error, emulating a broken io.Writer.
type streamShortWriter struct {
	calls int
}

func (w *streamShortWriter) Write(p []byte) (int, error) {
	w.calls++
	return len(p) / 2, nil
}

// streamFailWriter returns errStreamFail on the failAt-th Write call.
type streamFailWriter struct {
	err    error
	failAt int
	calls  int
}

func (w *streamFailWriter) Write(p []byte) (int, error) {
	idx := w.calls
	w.calls++
	if idx == w.failAt {
		return 0, w.err
	}
	return len(p), nil
}

var errStreamFail = errors.New("simulated stream failure")

// expectedCLIOutput reproduces what the CLI prints today: one header
// line for CSV/TSV and one Output line per record, each with a newline.
// WriteResults has to match these bytes exactly.
func expectedCLIOutput(gnp gnparser.GNparser, res []parsed.Parsed) string {
	f := gnp.Format()
	var b strings.Builder
	if header := parsed.HeaderCSV(f, gnp.WithDetails()); header != "" {
		b.WriteString(header)
		b.WriteByte('\n')
	}
	for i := range res {
		b.WriteString(res[i].Output(f, gnp.WithFlatOutput()))
		b.WriteByte('\n')
	}
	return b.String()
}

func testNames() []string {
	return []string{
		"Homo sapiens Linnaeus, 1758",
		"Bubo bubo",
		"Plantago major L.",
		"Aus bus cus",
	}
}

func TestGNparserWriteResults_Formats(t *testing.T) {
	for _, f := range []gnfmt.Format{
		gnfmt.CSV,
		gnfmt.TSV,
		gnfmt.CompactJSON,
		gnfmt.PrettyJSON,
	} {
		t.Run(f.String(), func(t *testing.T) {
			cfg := gnparser.NewConfig(
				gnparser.OptFormat(f),
				gnparser.OptIsTest(true),
			)
			gnp := gnparser.New(cfg)
			res := gnp.ParseNames(testNames())

			var buf bytes.Buffer
			err := gnp.WriteResults(&buf, res)
			assert.Nil(t, err)
			assert.Equal(t, expectedCLIOutput(gnp, res), buf.String())
		})
	}
}

func TestGNparserWriteResults_FlattenConfig(t *testing.T) {
	names := testNames()[:1]

	t.Run("flatten enabled by config", func(t *testing.T) {
		gnp := gnparser.New(gnparser.NewConfig(
			gnparser.OptFormat(gnfmt.CompactJSON),
			gnparser.OptWithFlatOutput(true),
			gnparser.OptIsTest(true),
		))
		res := gnp.ParseNames(names)

		var buf bytes.Buffer
		err := gnp.WriteResults(&buf, res)
		assert.Nil(t, err)
		assert.True(t, gnp.WithFlatOutput())
		assert.Contains(t, buf.String(), `"canonicalSimple"`)
		assert.NotContains(t, buf.String(), `"canonical":`)
	})

	t.Run("nested output without flatten", func(t *testing.T) {
		gnp := gnparser.New(gnparser.NewConfig(
			gnparser.OptFormat(gnfmt.CompactJSON),
			gnparser.OptIsTest(true),
		))
		res := gnp.ParseNames(names)

		var buf bytes.Buffer
		err := gnp.WriteResults(&buf, res)
		assert.Nil(t, err)
		assert.False(t, gnp.WithFlatOutput())
		assert.Contains(t, buf.String(), `"canonical":`)
		assert.NotContains(t, buf.String(), `"canonicalSimple"`)
	})
}

func TestGNparserWriteResults_DetailsConfig(t *testing.T) {
	gnp := gnparser.New(gnparser.NewConfig(
		gnparser.OptFormat(gnfmt.CSV),
		gnparser.OptWithDetails(true),
		gnparser.OptIsTest(true),
	))
	res := gnp.ParseNames(testNames())

	var buf bytes.Buffer
	err := gnp.WriteResults(&buf, res)
	assert.Nil(t, err)
	assert.True(t, gnp.WithDetails())

	r := csv.NewReader(strings.NewReader(buf.String()))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	assert.Nil(t, err)
	// header + one row per name
	assert.Len(t, records, len(res)+1)
	assert.Len(t, records[0], 36)
	for _, rec := range records[1:] {
		assert.Len(t, rec, 36)
	}
}

func TestGNparserWriteResults_Empty(t *testing.T) {
	t.Run("CSV empty keeps a valid header", func(t *testing.T) {
		gnp := gnparser.New(gnparser.NewConfig(gnparser.OptFormat(gnfmt.CSV)))
		var buf bytes.Buffer
		err := gnp.WriteResults(&buf, nil)
		assert.Nil(t, err)
		assert.Equal(t,
			parsed.HeaderCSV(gnfmt.CSV, gnp.WithDetails())+"\n",
			buf.String(),
		)
	})

	t.Run("JSON empty writes nothing", func(t *testing.T) {
		gnp := gnparser.New(gnparser.NewConfig(
			gnparser.OptFormat(gnfmt.CompactJSON),
		))
		var buf bytes.Buffer
		err := gnp.WriteResults(&buf, nil)
		assert.Nil(t, err)
		assert.Equal(t, "", buf.String())
	})
}

func TestGNparserWriteResults_NilWriter(t *testing.T) {
	gnp := gnparser.New(gnparser.NewConfig())
	err := gnp.WriteResults(nil, gnp.ParseNames(testNames()))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestGNparserWriteResults_InvalidFormat(t *testing.T) {
	for _, f := range []gnfmt.Format{gnfmt.FormatNone, gnfmt.Format(999)} {
		t.Run(f.String(), func(t *testing.T) {
			gnp := gnparser.New(gnparser.NewConfig(gnparser.OptFormat(f)))
			var buf bytes.Buffer
			err := gnp.WriteResults(&buf, gnp.ParseNames(testNames()))
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "invalid output format")
			assert.Equal(t, "", buf.String())
		})
	}
}

func TestGNparserWriteResults_ShortWrite(t *testing.T) {
	gnp := gnparser.New(gnparser.NewConfig(gnparser.OptFormat(gnfmt.CSV)))
	w := &streamShortWriter{}
	err := gnp.WriteResults(w, gnp.ParseNames(testNames()))
	assert.Error(t, err)
	assert.True(t, errors.Is(err, io.ErrShortWrite))
	assert.Equal(t, 1, w.calls)
}

func TestGNparserWriteResults_WriterError(t *testing.T) {
	gnp := gnparser.New(gnparser.NewConfig(
		gnparser.OptFormat(gnfmt.CompactJSON),
	))
	w := &streamFailWriter{err: errStreamFail, failAt: 1}
	err := gnp.WriteResults(w, gnp.ParseNames(testNames()))
	assert.Error(t, err)
	assert.True(t, errors.Is(err, errStreamFail))
	assert.Equal(t, 2, w.calls)
}

func TestGNparserWriteResults_ConcurrentIndependentWriters(t *testing.T) {
	// One shared, read-only GNparser instance and one shared read-only
	// result set; every goroutine owns an independent io.Writer.
	gnp := gnparser.New(gnparser.NewConfig(
		gnparser.OptFormat(gnfmt.CompactJSON),
		gnparser.OptWithFlatOutput(true),
		gnparser.OptIsTest(true),
	))
	res := gnp.ParseNames(testNames())
	want := expectedCLIOutput(gnp, res)

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errCh := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			var buf bytes.Buffer
			if err := gnp.WriteResults(&buf, res); err != nil {
				errCh <- err
				return
			}
			if buf.String() != want {
				errCh <- errors.New("concurrent output mismatch")
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		assert.Nil(t, err)
	}
}

func TestGNparserWriteResults_PoolIndependentWriters(t *testing.T) {
	// Pool-style usage: each goroutine checks out its own GNparser
	// instance and writes into a separate writer.
	formats := []gnfmt.Format{
		gnfmt.CSV, gnfmt.TSV, gnfmt.CompactJSON, gnfmt.PrettyJSON,
	}
	pool := gnparser.NewPool(gnparser.NewConfig(gnparser.OptIsTest(true)), 4)
	names := testNames()

	const goroutines = 40
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errCh := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			gnp := <-pool
			// ChangeConfig has a value receiver; capture the returned copy.
			gnp = gnp.ChangeConfig(
				gnparser.OptFormat(formats[i%len(formats)]),
			)
			// Use the checked-out instance fully before returning it to
			// the pool, so parser engines are never shared concurrently.
			res := gnp.ParseNames(names)
			var buf bytes.Buffer
			err := gnp.WriteResults(&buf, res)
			pool <- gnp
			if err != nil {
				errCh <- err
				return
			}
			if buf.String() != expectedCLIOutput(gnp, res) {
				errCh <- errors.New("pool output mismatch")
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		assert.Nil(t, err)
	}
}
