package media

import (
	"io"
	"os"
	"testing"
)

type countingReader struct {
	r     io.ReaderAt
	reads int
}

func (c *countingReader) ReadAt(p []byte, off int64) (int, error) {
	c.reads++
	return c.r.ReadAt(p, off)
}

// WRENTV_TEST_MKV=/path/to/file.mkv go test ./internal/media -run Cues -v
func TestMatroskaCues(t *testing.T) {
	p := os.Getenv("WRENTV_TEST_MKV")
	if p == "" {
		t.Skip("WRENTV_TEST_MKV not set")
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, _ := f.Stat()
	cr := &countingReader{r: f}
	kf, err := MatroskaCues(cr, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	// Remote sources pay a round trip per read.
	if cr.reads > 6 {
		t.Errorf("%d reads; want a handful", cr.reads)
	}
	if len(kf) < 2 {
		t.Fatalf("too few cues: %v", kf)
	}
	t.Logf("%d cues: %v", len(kf), kf[:min(len(kf), 12)])
}
