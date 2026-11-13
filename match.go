package main

import (
	"fmt"
	"strings"
)

// span is a place in a buffer to replace: bytes [start, end) and the text
// that goes there.
type span struct {
	start, end int
	text       string
	note       string // how it matched, when not exactly
}

// lineIndex holds the start offset of every line of a text.
type lineIndex struct {
	text   string
	starts []int
}

func newLineIndex(text string) *lineIndex {
	starts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' && i+1 < len(text) {
			starts = append(starts, i+1)
		}
	}
	if text == "" {
		starts = nil
	}
	return &lineIndex{text: text, starts: starts}
}

func (x *lineIndex) count() int { return len(x.starts) }

// line returns line i (0-based) without its newline.
func (x *lineIndex) line(i int) string {
	return x.text[x.starts[i]:x.lineEnd(i)]
}

// lineEnd is the offset of line i's newline (or the end of the text).
func (x *lineIndex) lineEnd(i int) int {
	if i+1 < len(x.starts) {
		return x.starts[i+1] - 1
	}
	if strings.HasSuffix(x.text, "\n") {
		return len(x.text) - 1
	}
	return len(x.text)
}

// lineAt is the 0-based line containing offset off.
func (x *lineIndex) lineAt(off int) int {
	lo, hi := 0, len(x.starts)-1
	for lo < hi {
		m := (lo + hi + 1) / 2
		if x.starts[m] <= off {
			lo = m
		} else {
			hi = m - 1
		}
	}
	return lo
}

// exactAll returns the offset of every occurrence of old in s, overlapping
// ones included, so that a count of one means old is unique.
func exactAll(s, old string) []int {
	var out []int
	for i := 0; i <= len(s)-len(old); {
		j := strings.Index(s[i:], old)
		if j < 0 {
			break
		}
		out = append(out, i+j)
		i += j + 1 // overlapping ones count too: "aa" occurs twice in "aaa"
	}
	return out
}

// aligned drops the matches of an old text that starts with spaces or tabs
// where it starts inside a run of them: "    foo" doesn't match the end of
// "        foo", which is a line with other indentation, not this one.
func aligned(s, old string, offs []int) []int {
	if old == "" || old[0] != ' ' && old[0] != '\t' {
		return offs
	}
	out := offs[:0:0]
	for _, o := range offs {
		if o == 0 || s[o-1] != ' ' && s[o-1] != '\t' {
			out = append(out, o)
		}
	}
	return out
}

// overlap tells whether any two occurrences of a text of length n overlap.
func overlap(offs []int, n int) bool {
	for k := 1; k < len(offs); k++ {
		if offs[k] < offs[k-1]+n {
			return true
		}
	}
	return false
}

func trimRight(s string) string { return strings.TrimRight(s, " \t\r") }

func indentOf(s string) string { return s[:len(s)-len(strings.TrimLeft(s, " \t"))] }

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// tolerant finds old in the lines of scope when it isn't there exactly:
// first ignoring whitespace at the ends of lines, then with every line's
// indentation shifted by the same prefix (the new text is shifted with it).
// It returns the matches of the first way that finds any; more than one
// means old is ambiguous. offs are the matches' start offsets in scope.
func tolerant(scope *lineIndex, old, new string) (spans []span, err error) {
	ol := strings.Split(old, "\n")
	n := len(ol)
	if n == 0 || scope.count() < n {
		return nil, nil
	}
	for i := 0; i+n <= scope.count(); i++ {
		ok := true
		for k := 0; k < n && ok; k++ {
			ok = trimRight(scope.line(i+k)) == trimRight(ol[k])
		}
		if ok {
			text, cr := keepCR(scope, i, n, new)
			note := "matched ignoring spaces at the ends of lines"
			if cr {
				note = "matched ignoring the \\r of the file's \\r\\n line endings (kept in the new text)"
			}
			spans = append(spans, span{start: scope.starts[i], end: scope.lineEnd(i + n - 1), text: text, note: note})
		}
	}
	if len(spans) > 0 {
		return spans, nil
	}
	first := -1
	for k, l := range ol {
		if !blank(l) {
			first = k
			break
		}
	}
	if first < 0 {
		return nil, nil
	}
	// every place that matches counts, even one whose new text can't be
	// shifted: otherwise an ambiguous block could apply to the other place
	type cand struct {
		i      int
		prefix string
		add    bool
	}
	var cands []cand
	for i := 0; i+n <= scope.count(); i++ {
		if prefix, add, ok := shifted(scope, i, ol, first); ok {
			cands = append(cands, cand{i, prefix, add})
		}
	}
	for _, c := range cands {
		text := new
		if len(cands) == 1 {
			if text, err = reindent(new, c.prefix, c.add); err != nil {
				return nil, err
			}
			text, _ = keepCR(scope, c.i, n, text)
		}
		spans = append(spans, span{start: scope.starts[c.i], end: scope.lineEnd(c.i + n - 1), text: text, note: "matched " + describeShift(c.prefix, c.add)})
	}
	return spans, nil
}

// keepCR gives the new text the \r line endings of the n lines from line i
// it replaces, when all of them have one (a file with mixed line endings).
func keepCR(scope *lineIndex, i, n int, text string) (string, bool) {
	for k := 0; k < n; k++ {
		if !strings.HasSuffix(scope.line(i+k), "\r") {
			return text, false
		}
	}
	if text == "" {
		return text, true
	}
	return strings.ReplaceAll(text, "\n", "\r\n") + "\r", true
}

// shifted tells whether the lines of scope from line i are ol with the
// same whitespace prefix added to (add) or removed from every non-blank line.
func shifted(scope *lineIndex, i int, ol []string, first int) (prefix string, add, ok bool) {
	fl, o := trimRight(scope.line(i+first)), trimRight(ol[first])
	fi, oi := indentOf(fl), indentOf(o)
	if fl[len(fi):] != o[len(oi):] {
		return "", false, false
	}
	switch {
	case len(fi) > len(oi) && strings.HasSuffix(fi, oi):
		prefix, add = fi[:len(fi)-len(oi)], true
	case len(oi) > len(fi) && strings.HasSuffix(oi, fi):
		prefix = oi[:len(oi)-len(fi)]
	default:
		return "", false, false
	}
	for k, l := range ol {
		f := trimRight(scope.line(i + k))
		l = trimRight(l)
		switch {
		case blank(l):
			if !blank(f) {
				return "", false, false
			}
		case add:
			if f != prefix+l {
				return "", false, false
			}
		default:
			if l != prefix+f {
				return "", false, false
			}
		}
	}
	return prefix, add, true
}

// reindent adds prefix to (add) or removes it from every non-blank line.
func reindent(text, prefix string, add bool) (string, error) {
	if text == "" {
		return text, nil
	}
	ls := strings.Split(text, "\n")
	for k, l := range ls {
		if blank(l) {
			continue
		}
		if add {
			ls[k] = prefix + l
		} else if strings.HasPrefix(l, prefix) {
			ls[k] = l[len(prefix):]
		} else {
			return "", fmt.Errorf("the old text matches %s, but line %d of the new text has less indentation than that, so it can't be shifted with it; write the block with the file's indentation", describeShift(prefix, false), k+1)
		}
	}
	return strings.Join(ls, "\n"), nil
}

// describeShift says how far the file's indentation is from the block's:
// "8 spaces further in", "1 tab further out".
func describeShift(prefix string, add bool) string {
	way := "further out"
	if add {
		way = "further in"
	}
	spaces, tabs := strings.Count(prefix, " "), strings.Count(prefix, "\t")
	var what string
	switch {
	case tabs == 0:
		what = plural(spaces, "space")
	case spaces == 0:
		what = plural(tabs, "tab")
	default:
		what = fmt.Sprintf("%s and %s", plural(tabs, "tab"), plural(spaces, "space"))
	}
	return what + " " + way
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// near is the place in a scope most like a block's old text.
type near struct {
	first, last int // 0-based lines of the scope
	at          int // the old text's first line (0-based) that differs; -1 if none
	file, block string
}

// nearest finds the lines of scope most like old: the window that has the
// most of old's lines (spaces at the ends ignored), else the line most like
// old's first non-blank line. ok is false when nothing is alike at all.
func nearest(scope *lineIndex, old string) (near, bool) {
	ol := strings.Split(old, "\n")
	n := len(ol)
	if scope.count() == 0 {
		return near{}, false
	}
	pos := map[string][]int{}
	for i := 0; i < scope.count(); i++ {
		t := strings.TrimSpace(scope.line(i))
		if t != "" {
			pos[t] = append(pos[t], i)
		}
	}
	votes := map[int]int{}
	best, bestVotes := 0, 0
	for k, l := range ol {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		for _, p := range pos[t] {
			s := p - k
			votes[s]++
			if v := votes[s]; v > bestVotes || v == bestVotes && s < best {
				best, bestVotes = s, v
			}
		}
	}
	if bestVotes == 0 {
		k := 0
		for k < n && blank(ol[k]) {
			k++
		}
		if k == n {
			return near{}, false
		}
		want := strings.TrimSpace(ol[k])
		score := 0.0
		for i := 0; i < scope.count(); i++ {
			if d := dice(want, strings.TrimSpace(scope.line(i))); d > score {
				score, best = d, i-k
			}
		}
		if score < 0.4 {
			return near{}, false
		}
	}
	nr := near{at: -1}
	nr.first = max(best, 0)
	nr.last = min(best+n-1, scope.count()-1)
	for k := 0; k < n; k++ {
		i := best + k
		var f string
		have := i >= 0 && i < scope.count()
		if have {
			f = scope.line(i)
		}
		if !have || trimRight(f) != trimRight(ol[k]) {
			nr.at = k
			nr.block = ol[k]
			if have {
				nr.file = f
			} else if i < 0 {
				nr.file = "(before the start of the scope)"
			} else {
				nr.file = "(after the end of the scope)"
			}
			break
		}
	}
	return nr, true
}

// dice is the Sørensen–Dice coefficient of the character bigrams of a and b.
func dice(a, b string) float64 {
	if a == b {
		return 1
	}
	if len(a) < 2 || len(b) < 2 {
		return 0
	}
	bi := map[string]int{}
	for i := 0; i+2 <= len(a); i++ {
		bi[a[i:i+2]]++
	}
	both := 0
	for i := 0; i+2 <= len(b); i++ {
		if bi[b[i:i+2]] > 0 {
			bi[b[i:i+2]]--
			both++
		}
	}
	return 2 * float64(both) / float64(len(a)-1+len(b)-1)
}

// ordinal is "1st", "2nd", "3rd", "4th", …
func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}
