package media

import (
	"os"
	"testing"
)

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
	kf, err := MatroskaCues(f, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	if len(kf) < 2 {
		t.Fatalf("too few cues: %v", kf)
	}
	t.Logf("%d cues: %v", len(kf), kf[:min(len(kf), 12)])
}
