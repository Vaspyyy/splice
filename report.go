package main

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Vaspyyy/prism/outline"
	"github.com/Vaspyyy/prism/ref"
)

// hunk is a run of changed lines: oc lines from os in the old text became nc
// lines from ns in the new one (0-based).
type hunk struct{ os, oc, ns, nc int }

// maxD bounds the diff's work; past it the changed middle is one hunk.
const maxD = 2000

// diffLines compares two texts' lines (Myers' algorithm).
func diffLines(a, b []string) []hunk {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	A, B := a[pre:len(a)-suf], b[pre:len(b)-suf]
	if len(A) == 0 && len(B) == 0 {
		return nil
	}
	del, ins, ok := myers(A, B)
	if !ok {
		return []hunk{{pre, len(A), pre, len(B)}}
	}
	var hs []hunk
	for i, j := 0, 0; i < len(A) || j < len(B); {
		if i < len(A) && j < len(B) && !del[i] && !ins[j] {
			i, j = i+1, j+1
			continue
		}
		h := hunk{os: pre + i, ns: pre + j}
		for i < len(A) && del[i] {
			i, h.oc = i+1, h.oc+1
		}
		for j < len(B) && ins[j] {
			j, h.nc = j+1, h.nc+1
		}
		hs = append(hs, h)
	}
	return hs
}

// myers marks the lines of A deleted and of B inserted by a shortest edit.
// ok is false when that takes more than maxD steps.
func myers(A, B []string) (del, ins []bool, ok bool) {
	n, m := len(A), len(B)
	del, ins = make([]bool, n), make([]bool, m)
	if n == 0 || m == 0 {
		for i := range del {
			del[i] = true
		}
		for j := range ins {
			ins[j] = true
		}
		return del, ins, true
	}
	limit := min(n+m, maxD)
	off := limit + 1
	v := make([]int, 2*off+1)
	var trace [][]int // trace[d]: v over k in [-d, d] before step d
	for d := 0; d <= limit; d++ {
		snap := make([]int, 2*d+1)
		copy(snap, v[off-d:off+d+1])
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || k != d && v[off+k-1] < v[off+k+1] {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && A[x] == B[y] {
				x, y = x+1, y+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				backtrack(trace, n, m, del, ins)
				return del, ins, true
			}
		}
	}
	return nil, nil, false
}

func backtrack(trace [][]int, x, y int, del, ins []bool) {
	for d := len(trace) - 1; d > 0; d-- {
		v := trace[d]
		at := func(k int) int { return v[k+d] }
		k := x - y
		var pk int
		if k == -d || k != d && at(k-1) < at(k+1) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := at(pk)
		py := px - pk
		for x > px && y > py {
			x, y = x-1, y-1
		}
		if x == px {
			ins[py] = true
		} else {
			del[px] = true
		}
		x, y = px, py
	}
}

// change is what happened to one definition of a file.
type change struct {
	verb        string // changed, added, removed
	kind, name  string
	line, end   int // in the new text; 0 for a removed one
	plus, minus int
	first       int // the first new line touched, for ordering
	sigChanged  bool
}

// fileReport is a written (or checked) file's changes.
type fileReport struct {
	f        *file
	changes  []*change
	outside  [][2]int // changed lines outside any definition (new text, 1-based)
	plus     int
	minus    int
	newLines int
	defs     int
	check    string
}

// keyed names a definition for grouping: its ref target, ordinal included.
func keyed(ts []ref.Target) []string {
	ords := ref.Ordinals(ts)
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = strings.TrimPrefix(ref.Symbol(t.Path, t.Name, ords[i]).String(), t.Path+":")
	}
	return out
}

// summarize works out which definitions a file's edits changed.
func summarize(f *file) *fileReport {
	fr := &fileReport{f: f}
	newText := f.buf
	newSyms := outline.Parse(f.rel, []byte(newText), outline.MaxSize)
	newT := ref.Targets(f.rel, newSyms)
	if f.created {
		fr.newLines = len(splitLines(newText))
		fr.defs = len(newT)
		fr.plus = fr.newLines
		fr.check = check(f.rel, "", newText, false)
		return fr
	}
	oldText := string(f.orig)
	if f.crlf {
		oldText = strings.ReplaceAll(oldText, "\r\n", "\n")
	}
	oldSyms := outline.Parse(f.rel, []byte(oldText), outline.MaxSize)
	oldT := ref.Targets(f.rel, oldSyms)
	newK, oldK := keyed(newT), keyed(oldT)
	newAt := map[string]int{}
	for i, k := range newK {
		newAt[k] = i
	}
	oldAt := map[string]int{}
	for i, k := range oldK {
		oldAt[k] = i
	}
	byKey := map[string]*change{}
	get := func(key string, first int) *change {
		c := byKey[key]
		if c == nil {
			c = &change{name: key, first: first}
			byKey[key] = c
		}
		c.first = min(c.first, first)
		return c
	}
	var outside []int
	a, b := splitLines(oldText), splitLines(newText)
	newOwner, oldOwner := owners(b, newT, f.rel), owners(a, oldT, f.rel)
	for _, h := range diffLines(a, b) {
		fr.plus += h.nc
		fr.minus += h.oc
		for j := h.ns; j < h.ns+h.nc; j++ {
			if i := newOwner[j]; i >= 0 {
				get(newK[i], j+1).plus++
			} else if !blank(b[j]) {
				outside = append(outside, j+1)
			}
		}
		for j := h.os; j < h.os+h.oc; j++ {
			if i := oldOwner[j]; i >= 0 {
				get(oldK[i], h.ns+1).minus++
			} else if h.nc == 0 && !blank(a[j]) {
				outside = append(outside, min(h.ns+1, max(len(b), 1)))
			}
		}
	}
	for key, c := range byKey {
		ni, inNew := newAt[key]
		oi, inOld := oldAt[key]
		switch {
		case inNew && inOld:
			c.verb = "changed"
			c.sigChanged = newSyms[ni].Sig != oldSyms[oi].Sig // targets keep the outline's order
		case inNew:
			c.verb = "added"
		default:
			c.verb = "removed"
		}
		if inNew {
			c.kind, c.line, c.end = newT[ni].Kind, newT[ni].Line, newT[ni].End
		} else {
			c.kind = oldT[oi].Kind
		}
		fr.changes = append(fr.changes, c)
	}
	sort.Slice(fr.changes, func(i, j int) bool {
		if fr.changes[i].first != fr.changes[j].first {
			return fr.changes[i].first < fr.changes[j].first
		}
		return fr.changes[i].name < fr.changes[j].name
	})
	fr.outside = ranges(outside)
	fr.check = check(f.rel, oldText, newText, true)
	return fr
}

// owners gives each line (0-based) the target it belongs to, or -1: the
// innermost definition containing it, else the definition right below a
// run of non-blank lines it's in (a doc comment, decorators).
func owners(lines []string, ts []ref.Target, rel string) []int {
	starts := map[int]int{} // 1-based defining line -> target
	for i, t := range ts {
		if _, ok := starts[t.Line]; !ok {
			starts[t.Line] = i
		}
	}
	within := func(c, p int) bool { // target c is nested in target p
		return ts[c].Line > ts[p].Line && ts[c].End <= ts[p].End
	}
	inner := innermostAll(ts, len(lines))
	out := make([]int, len(lines))
	below := -1 // the definition a non-blank line here would belong to
	for j := len(lines) - 1; j >= 0; j-- {
		i := inner[j]
		o := i
		if !blank(lines[j]) && below >= 0 && (i < 0 || within(below, i)) {
			o = below
		}
		out[j] = o
		if t, ok := starts[j+1]; ok {
			below = t
		} else if blank(lines[j]) || o != below {
			below = -1
		}
	}
	return out
}

// innermostAll is ref.Innermost for every line (0-based) at once: the
// smallest target containing it, the first of equal ones. It takes time in
// proportion to the lines the targets span, not lines times targets.
func innermostAll(ts []ref.Target, n int) []int {
	span := func(i int) int { return max(ts[i].End, ts[i].Line) - ts[i].Line }
	order := make([]int, len(ts))
	for i := range order {
		order[i] = i
	}
	// widest first, and among equal ones the last first, so that the
	// innermost (and first) is written last
	sort.Slice(order, func(a, b int) bool {
		if sa, sb := span(order[a]), span(order[b]); sa != sb {
			return sa > sb
		}
		return order[a] > order[b]
	})
	out := make([]int, n)
	for j := range out {
		out[j] = -1
	}
	for _, i := range order {
		for l := ts[i].Line; l <= max(ts[i].End, ts[i].Line) && l <= n; l++ {
			if l >= 1 {
				out[l-1] = i
			}
		}
	}
	return out
}

// splitLines splits text into lines; a final newline doesn't start one more.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// ranges turns line numbers into sorted, merged runs.
func ranges(ls []int) [][2]int {
	sort.Ints(ls)
	var out [][2]int
	for _, l := range ls {
		if n := len(out); n > 0 && l <= out[n-1][1]+1 {
			out[n-1][1] = max(out[n-1][1], l)
			continue
		}
		out = append(out, [2]int{l, l})
	}
	return out
}

func lineRange(a, b int) string {
	if a == b {
		return fmt.Sprintf("L%d", a)
	}
	return fmt.Sprintf("L%d-%d", a, b)
}

// check says when an edit broke a Go or JSON file that parsed before. had
// says whether there was an old text to compare with.
func check(rel, oldText, newText string, had bool) string {
	parse := func(text string) string { return "" }
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go":
		parse = func(text string) string {
			fset := token.NewFileSet()
			if _, err := parser.ParseFile(fset, rel, text, parser.SkipObjectResolution); err != nil {
				return firstLine(err.Error())
			}
			return ""
		}
	case ".json":
		parse = func(text string) string {
			var v any
			err := json.Unmarshal([]byte(text), &v)
			if err == nil {
				return ""
			}
			if se, ok := err.(*json.SyntaxError); ok {
				line, col := lineCol(text, int(se.Offset))
				return fmt.Sprintf("%s:%d:%d: %v", rel, line, col, se)
			}
			return fmt.Sprintf("%s: %v", rel, err)
		}
	default:
		return ""
	}
	after := parse(newText)
	if after == "" || had && parse(oldText) != "" {
		return ""
	}
	return after
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// lineCol turns a byte offset into a 1-based line and column.
func lineCol(text string, off int) (int, int) {
	off = min(max(off, 0), len(text))
	line := strings.Count(text[:off], "\n") + 1
	col := off - strings.LastIndexByte(text[:off], '\n')
	return line, col
}
