package usenet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
)

// Video is a playable file found in a post.
type Video struct {
	Name   string
	Size   int64
	Layout *Layout
}

// head is what the first article of an NZB file says.
type head struct {
	name     string
	size     int64
	partSize int64
	data     []byte
	err      error
	// lazy is a RAR volume left unread for now: in a big archive the
	// middle volumes are laid out alike, so most are worked out from
	// their neighbours (see rarVideos).
	lazy bool
}

// ErrUnplayable means a post has no video that can be streamed.
var ErrUnplayable = errors.New("no streamable video in this post")

// Inspect lists the videos in a post, reading the first article of each
// file. RAR volumes are looked into; compressed or protected archives,
// 7z and zip can't be streamed and are skipped. keep, when set, skips
// files whose posted name says they aren't wanted (other episodes of a
// season pack), so only what might play is read.
func Inspect(ctx context.Context, pool *Pool, nzb *NZB, keep func(name string) bool) ([]Video, error) {
	var idx []int
	for i, f := range nzb.Files {
		name := f.Name()
		if junkExtRE.MatchString(name) || archive7zRE.MatchString(name) {
			continue
		}
		if _, rar := rarVolume(name); keep != nil && (rar || isVideo(name)) && !keep(name) {
			continue
		}
		idx = append(idx, i)
	}
	// A 60 GB remux in 100 MB volumes is over 600 files.
	if len(idx) == 0 || len(idx) > 2000 {
		return nil, ErrUnplayable
	}
	heads := make([]head, len(nzb.Files))
	read := func(files []int) {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 16)
		for _, i := range files {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				heads[i] = readHead(ctx, pool, nzb.Files[i])
			}(i)
		}
		wg.Wait()
	}
	// The first volume of a named RAR set says whether the archive can be
	// streamed at all; the rest are read only if it can. A big post has
	// a hundred or more volumes.
	// The first is the lowest numbered: "a.rar" before "a.r00" is 0, but
	// "a.part01.rar" is 1.
	first, low := map[string]int{}, map[string]int{}
	for _, i := range idx {
		name := nzb.Files[i].Name()
		if n, ok := rarVolume(name); ok {
			set := rarSet(name)
			if l, seen := low[set]; !seen || n < l {
				first[set], low[set] = i, n
			}
		}
	}
	var now, later []int
	for _, i := range idx {
		name := nzb.Files[i].Name()
		if _, ok := rarVolume(name); ok {
			if f, has := first[rarSet(name)]; has && f != i {
				later = append(later, i)
				continue
			}
		}
		now = append(now, i)
	}
	read(now)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(later) > 0 {
		bad := map[string]error{}
		for set, i := range first {
			h := heads[i]
			if h.err != nil {
				bad[set] = h.err
			} else if !isRAR(h.data) {
				continue // misnamed; read the rest and see
			} else if _, err := readRARVolume(bytes.NewReader(h.data), int64(len(h.data))); err != nil &&
				(errors.Is(err, errPacked) || errors.Is(err, errEncrypted)) {
				bad[set] = err
			}
		}
		var rest []int
		bySet := map[string][]int{}
		for _, i := range later {
			set := rarSet(nzb.Files[i].Name())
			if err := bad[set]; err != nil {
				heads[i] = head{err: err}
				continue
			}
			bySet[set] = append(bySet[set], i)
		}
		for _, vols := range bySet {
			rest = append(rest, unreadMiddle(nzb, heads, vols)...)
		}
		read(rest)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}

	var videos []Video
	sets := map[string][]int{}
	var setOrder []string
	var firstErr error
	for _, i := range idx {
		h := heads[i]
		if h.err != nil {
			if firstErr == nil {
				firstErr = h.err
			}
			continue
		}
		name := bestName(h.name, nzb.Files[i].Name())
		switch {
		case isRAR(h.data) || h.lazy:
			key := rarSet(name)
			if _, ok := rarVolume(name); !ok {
				key = "\x00obfuscated" // nameless volumes: one archive, in post order
			}
			if sets[key] == nil {
				setOrder = append(setOrder, key)
			}
			sets[key] = append(sets[key], i)
		case isVideo(name) || looksLikeVideo(h.data):
			if sampleRE.MatchString(name) {
				continue
			}
			if !isVideo(name) {
				name += sniffExt(h.data)
			}
			lay := &Layout{Name: name, Size: h.size, Files: []FileLayout{{Index: i, Size: h.size, PartSize: h.partSize}},
				Pieces: []Piece{{File: 0, Off: 0, Len: h.size}}}
			videos = append(videos, Video{Name: name, Size: h.size, Layout: lay})
		}
	}
	for _, key := range setOrder {
		vs, err := rarVideos(ctx, pool, nzb, heads, sets[key], key != "\x00obfuscated")
		if err != nil {
			if firstErr == nil || errors.Is(firstErr, ErrMissing) {
				firstErr = err
			}
			continue
		}
		videos = append(videos, vs...)
	}
	if len(videos) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, ErrUnplayable
	}
	slices.SortStableFunc(videos, func(a, b Video) int {
		switch {
		case a.Size > b.Size:
			return -1
		case a.Size < b.Size:
			return 1
		}
		return 0
	})
	return videos, nil
}

// unreadMiddle takes the volumes after the first of a big archive and
// returns the ones to read now: the two at each end. The rest are marked
// lazy. Volumes that aren't numbered one after another are all read.
func unreadMiddle(nzb *NZB, heads []head, vols []int) []int {
	if len(vols) <= 8 {
		return vols
	}
	vols = slices.Clone(vols)
	num := func(i int) int { n, _ := rarVolume(nzb.Files[i].Name()); return n }
	slices.SortFunc(vols, func(a, b int) int { return num(a) - num(b) })
	for k := 1; k < len(vols); k++ {
		if num(vols[k]) != num(vols[k-1])+1 {
			return vols
		}
	}
	for _, i := range vols[2 : len(vols)-2] {
		heads[i] = head{lazy: true}
	}
	return append(vols[:2:2], vols[len(vols)-2:]...)
}

func readHead(ctx context.Context, pool *Pool, f NZBFile) head {
	body, err := pool.Body(ctx, f.Segments[0].ID)
	if err != nil {
		return head{err: err}
	}
	p, err := decodeYEnc(body)
	if err != nil {
		return head{err: err}
	}
	h := head{name: p.Name, size: p.Size, partSize: int64(len(p.Data)), data: p.Data}
	if h.size <= 0 {
		h.size = int64(len(p.Data))
	}
	// Every article but the last is the same size; the count has to agree.
	if len(f.Segments) > 1 && (h.partSize <= 0 || (h.size+h.partSize-1)/h.partSize != int64(len(f.Segments))) {
		return head{err: fmt.Errorf("file %q isn't split evenly", p.Name)}
	}
	if len(f.Segments) == 1 {
		h.partSize = max(h.size, 1)
	}
	return h
}

// bestName prefers the name that looks real: posts often hide one of them.
func bestName(yenc, subject string) string {
	known := func(n string) bool {
		_, rar := rarVolume(n)
		return isVideo(n) || rar
	}
	switch {
	case known(yenc):
		return yenc
	case known(subject):
		return subject
	case yenc != "":
		return yenc
	}
	return subject
}

func looksLikeVideo(b []byte) bool {
	return sniffExt(b) != ""
}

func sniffExt(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0x1a, 0x45, 0xdf, 0xa3}):
		return ".mkv"
	case len(b) >= 12 && string(b[4:8]) == "ftyp":
		return ".mp4"
	case len(b) >= 189 && b[0] == 0x47 && b[188] == 0x47:
		return ".ts"
	}
	return ""
}

// rarVideos reads one archive's volumes and returns the videos stored in it.
func rarVideos(ctx context.Context, pool *Pool, nzb *NZB, heads []head, files []int, named bool) ([]Video, error) {
	type vol struct {
		file int
		name string
		num  int
	}
	vols := make([]vol, len(files))
	for k, i := range files {
		name := bestName(heads[i].name, nzb.Files[i].Name())
		n, ok := rarVolume(name)
		if !ok {
			n = k
		}
		vols[k] = vol{file: i, name: name, num: n}
	}
	slices.SortStableFunc(vols, func(a, b vol) int { return a.num - b.num })

	lay := &Layout{}
	for _, v := range vols {
		h := heads[v.file]
		lay.Files = append(lay.Files, FileLayout{Index: v.file, Size: h.size, PartSize: h.partSize})
	}
	f := NewFile(pool, nzb, lay)
	f.max, f.ctx = 16, ctx
	put := func(k int, data []byte) {
		e := &segEntry{done: make(chan struct{}), data: data}
		close(e.done)
		f.mu.Lock()
		f.cache[segKey{k, 0}] = e
		f.mu.Unlock()
	}
	// The first article of each volume read so far is already here.
	for k, v := range vols {
		if !heads[v.file].lazy {
			put(k, heads[v.file].data)
		}
	}
	// Volumes are read side by side: a header that doesn't fit in the
	// first article needs another fetch, and big posts have a hundred.
	infos := make([]*rarVolumeInfo, len(vols))
	errs := make([]error, len(vols))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for k := range vols {
		if heads[vols[k].file].lazy {
			continue
		}
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			infos[k], errs[k] = readRARVolume(fileReader{f: f, file: k}, lay.Files[k].Size)
		}(k)
	}
	wg.Wait()
	for k, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("%s: %w", vols[k].name, err)
		}
	}
	// Unread volumes between two read ones that agree are taken to agree
	// too; where the two differ, the one halfway is read and each half
	// looked at again. A 600-volume archive takes about thirty reads.
	load := func(k int) error {
		h := readHead(ctx, pool, nzb.Files[vols[k].file])
		if h.err != nil {
			return fmt.Errorf("%s: %w", vols[k].name, h.err)
		}
		lay.Files[k] = FileLayout{Index: vols[k].file, Size: h.size, PartSize: h.partSize}
		put(k, h.data)
		info, err := readRARVolume(fileReader{f: f, file: k}, h.size)
		if err != nil {
			return fmt.Errorf("%s: %w", vols[k].name, err)
		}
		infos[k] = info
		return nil
	}
	alike := func(a, b int) bool {
		fa, fb := lay.Files[a], lay.Files[b]
		if fa.Size != fb.Size || fa.PartSize != fb.PartSize || !slices.Equal(infos[a].Entries, infos[b].Entries) {
			return false
		}
		segs := len(nzb.Files[vols[a].file].Segments)
		for k := a + 1; k < b; k++ {
			if len(nzb.Files[vols[k].file].Segments) != segs {
				return false
			}
		}
		return true
	}
	var fill func(a, b int) error
	fill = func(a, b int) error {
		if b-a < 2 {
			return nil
		}
		// Long runs are split anyway, as a check on the guess.
		if b-a <= 32 && alike(a, b) {
			for k := a + 1; k < b; k++ {
				lay.Files[k] = FileLayout{Index: vols[k].file, Size: lay.Files[a].Size, PartSize: lay.Files[a].PartSize}
				infos[k] = &rarVolumeInfo{Entries: slices.Clone(infos[a].Entries), Number: -1}
			}
			return nil
		}
		m := (a + b) / 2
		if err := load(m); err != nil {
			return err
		}
		var e1, e2 error
		var fw sync.WaitGroup
		fw.Add(1)
		go func() { defer fw.Done(); e1 = fill(a, m) }()
		e2 = fill(m, b)
		fw.Wait()
		return errors.Join(e1, e2)
	}
	for a := 0; a < len(vols); a++ {
		if !heads[vols[a].file].lazy {
			continue
		}
		b := a
		for b < len(vols) && heads[vols[b].file].lazy {
			b++
		}
		if a == 0 || b == len(vols) {
			return nil, errors.New("RAR: unread volumes at the end of the archive")
		}
		if err := fill(a-1, b); err != nil {
			return nil, err
		}
		a = b
	}
	// Nameless volumes are in post order, unless RAR5 numbers them.
	if !named {
		order := make([]int, len(infos))
		for k := range order {
			order[k] = k
		}
		if !slices.ContainsFunc(infos, func(i *rarVolumeInfo) bool { return i.Number < 0 }) {
			slices.SortStableFunc(order, func(a, b int) int { return infos[a].Number - infos[b].Number })
			files2, infos2 := make([]FileLayout, len(order)), make([]*rarVolumeInfo, len(order))
			for k, o := range order {
				files2[k], infos2[k] = lay.Files[o], infos[o]
			}
			lay.Files, infos = files2, infos2
		}
	}
	joined, err := joinRAR(infos)
	if err != nil {
		return nil, err
	}
	var out []Video
	for _, j := range joined {
		name := path.Base(strings.ReplaceAll(j.Name, `\`, "/"))
		if !isVideo(name) || sampleRE.MatchString(name) {
			continue
		}
		// Only the volumes this file uses go in its layout.
		l := &Layout{Name: name, Size: j.Size}
		remap := map[int]int{}
		for _, pc := range j.Pieces {
			if _, ok := remap[pc.File]; !ok {
				remap[pc.File] = len(l.Files)
				l.Files = append(l.Files, lay.Files[pc.File])
			}
			l.Pieces = append(l.Pieces, Piece{File: remap[pc.File], Off: pc.Off, Len: pc.Len})
		}
		out = append(out, Video{Name: name, Size: j.Size, Layout: l})
	}
	if len(out) == 0 {
		return nil, ErrUnplayable
	}
	return out, nil
}

// Alive checks a spread of a whole post's articles, leaving out par2 and
// other extras. It takes a second where reading a taken-down post's files
// takes many, so dead posts are dropped before they're inspected.
// keep is as for Inspect.
func Alive(ctx context.Context, pool *Pool, nzb *NZB, keep func(name string) bool, samples int) (bool, error) {
	var all []string
	for _, f := range nzb.Files {
		name := f.Name()
		if junkExtRE.MatchString(name) {
			continue
		}
		if _, rar := rarVolume(name); keep != nil && (rar || isVideo(name)) && !keep(name) {
			continue
		}
		for _, s := range f.Segments {
			all = append(all, s.ID)
		}
	}
	return statSpread(ctx, pool, all, samples)
}

// Available checks a spread of a video's articles on the news servers.
// Posts lose articles to takedowns; this catches most dead ones before
// anyone presses play.
func Available(ctx context.Context, pool *Pool, nzb *NZB, lay *Layout, samples int) (bool, error) {
	var all []string
	for _, fl := range lay.Files {
		for _, s := range nzb.Files[fl.Index].Segments {
			all = append(all, s.ID)
		}
	}
	return statSpread(ctx, pool, all, samples)
}

func statSpread(ctx context.Context, pool *Pool, all []string, samples int) (bool, error) {
	if len(all) == 0 {
		return false, nil
	}
	var pickIDs []string
	if len(all) <= samples {
		pickIDs = all
	} else {
		for k := 0; k < samples; k++ {
			pickIDs = append(pickIDs, all[k*(len(all)-1)/(samples-1)])
		}
	}
	// Split across a few connections.
	const lanes = 3
	var wg sync.WaitGroup
	results := make([]bool, lanes)
	errs := make([]error, lanes)
	for l := 0; l < lanes; l++ {
		var chunk []string
		for k := l; k < len(pickIDs); k += lanes {
			chunk = append(chunk, pickIDs[k])
		}
		if len(chunk) == 0 {
			results[l] = true
			continue
		}
		wg.Add(1)
		go func(l int, ids []string) {
			defer wg.Done()
			results[l], errs[l] = pool.Stat(ctx, ids)
		}(l, chunk)
	}
	wg.Wait()
	for l := range results {
		if errs[l] != nil {
			return false, errs[l]
		}
		if !results[l] {
			return false, nil
		}
	}
	return true, nil
}
