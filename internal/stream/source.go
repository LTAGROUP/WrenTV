// Package stream serves media to clients: direct (byte-range) play, on-the-fly
// HLS remux (no re-encode, preserves Dolby Vision / Atmos), and hardware
// accelerated HLS transcode.
package stream

import (
	"io"
	"net/http"
	"os"
	"time"
)

// Source is something playable: a local file, a torrent file, or a debrid link.
type Source interface {
	// Input is what ffmpeg/ffprobe should read (a path or an http URL).
	Input() string
	// ReaderAt gives random access for index parsing (may be nil).
	ReaderAt() io.ReaderAt
	Size() int64
	// ServeHTTP serves the raw bytes with Range support.
	ServeHTTP(w http.ResponseWriter, r *http.Request)
	Close() error
}

type FileSource struct {
	path string
	f    *os.File
	size int64
	mod  time.Time
}

func OpenFile(path string) (*FileSource, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &FileSource{path: path, f: f, size: st.Size(), mod: st.ModTime()}, nil
}

func (s *FileSource) Input() string         { return s.path }
func (s *FileSource) ReaderAt() io.ReaderAt { return s.f }
func (s *FileSource) Size() int64           { return s.size }
func (s *FileSource) Close() error          { return s.f.Close() }

func (s *FileSource) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A fresh handle per request: ServeContent seeks, and requests run concurrently.
	f, err := os.Open(s.path)
	if err != nil {
		http.Error(w, "source unavailable", http.StatusGone)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", ContentType(s.path))
	http.ServeContent(w, r, "", s.mod, f)
}

// ContentType maps common media extensions.
func ContentType(name string) string {
	switch ext(name) {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".ts", ".m2ts":
		return "video/mp2t"
	case ".avi":
		return "video/x-msvideo"
	}
	return "application/octet-stream"
}

func ext(name string) string {
	for i := len(name) - 1; i >= 0 && name[i] != '/'; i-- {
		if name[i] == '.' {
			b := []byte(name[i:])
			for j := range b {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return ""
}
