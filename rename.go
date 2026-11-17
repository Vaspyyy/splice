package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Vaspyyy/prism/ref"
	"github.com/Vaspyyy/prism/repo"
)

// renameList is how many changed lines a rename lists before it only
// counts them per definition.
const renameList = 60

// renaming is one splice --rename: a word replaced by another wherever it
// stands in the places named, with every changed line shown, so that a hit
// that wasn't meant is seen at once.
type renaming struct {
	o       options
	s       *session
	hits    map[*file]map[int]bool // where the old word starts, in each file's buffer
	not     []*exclusion
	places  int      // places named or found in folders
	skipped []string // files in a folder that couldn't be read
	inWords int      // occurrences inside longer words, which a whole-word rename leaves
	inFirst string   // the first of them, as path:line
}

// exclusion is one --not: a folder, or a part of a file.
type exclusion struct {
	arg    string
	dir    string // a folder: everything under it
	f      *file
	lo, hi int // or the bytes [lo, hi) of f's buffer
	left   int // the occurrences it left out
}

// wordRune reports whether r can be part of a name.
func wordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// wordHits returns where old stands in s, left to right without overlaps.
// Unless inside is set, only as a word of its own: an end of old that is
// part of a name mustn't have more of a name next to it.
func wordHits(s, old string, inside bool) []int {
	first, _ := utf8.DecodeRuneInString(old)
	last, _ := utf8.DecodeLastRuneInString(old)
	var out []int
	for i := 0; ; {
		j := strings.Index(s[i:], old)
		if j < 0 {
			return out
		}
		at := i + j
		end := at + len(old)
		ok := inside
		if !ok {
			before, _ := utf8.DecodeLastRuneInString(s[:at])
			after, _ := utf8.DecodeRuneInString(s[end:])
			ok = !(wordRune(first) && at > 0 && wordRune(before)) && !(wordRune(last) && end < len(s) && wordRune(after))
		}
		if ok {
			out = append(out, at)
			i = end
		} else {
			i = at + 1
		}
	}
}

// under lists the files below the folder abs: the ones git sees when the
// folder is in a repository (tracked and untracked, not ignored), else
// every file but those in .git.
func (rn *renaming) under(abs string) []string {
	s := rn.s
	if s.info.Git {
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			if rel, ok := s.info.Rel(real); ok {
				var out []string
				for _, f := range s.gitFiles() {
					if rel == "." || rel == "" || strings.HasPrefix(f, rel+"/") {
						out = append(out, s.info.Abs(f))
					}
				}
				return out
			}
		}
	}
	var out []string
	filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			rn.skipped = append(rn.skipped, p)
		case d.IsDir() && d.Name() == ".git":
			return filepath.SkipDir
		case d.Type().IsRegular():
			out = append(out, p)
		}
		return nil
	})
	return out
}

// abs is a path as given on the command line, made absolute.
func (rn *renaming) abs(p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(rn.s.cwd, p)
	}
	return filepath.Clean(p)
}

// part opens the file a place names and finds the part of it that is
// meant. A place that names nothing fails, like a block's header.
func (rn *renaming) part(b *block, place string) (*file, scopeOf, bool) {
	s := rn.s
	r := ref.Parse(place, s.info.IsRev)
	if r.Rev != "" {
		s.fail(b, fmt.Sprintf("it names %s as of %s; splice edits the files as they are now: leave out @%s", r.Path, r.Rev, r.Rev))
		return nil, scopeOf{}, false
	}
	f, msg, detail := s.open(r.Path, false)
	if msg != "" {
		s.fail(b, msg, detail...)
		return nil, scopeOf{}, false
	}
	if !f.exists {
		s.fail(b, fmt.Sprintf("no file %s", f.rel), s.similarFiles(r.Path)...)
		return nil, scopeOf{}, false
	}
	sc, ok := s.scope(f, b, r)
	return f, sc, ok
}

// exclude reads one --not.
func (rn *renaming) exclude(arg string) {
	x := &exclusion{arg: arg}
	rn.not = append(rn.not, x)
	if fi, err := os.Stat(rn.abs(arg)); err == nil && fi.IsDir() {
		x.dir = rn.abs(arg)
		if real, err := filepath.EvalSymlinks(x.dir); err == nil {
			x.dir = real
		}
		return
	}
	// A line number is that line: the report lists lines by number. (In a
	// place to rename in, as in a block's header, it is the definition
	// around the line.)
	if r := ref.Parse(arg, rn.s.info.IsRev); r.HasTarget && r.Name == "" && r.Line > 0 && r.End == 0 {
		arg = fmt.Sprintf("%s:%d-%d", r.Path, r.Line, r.Line)
	}
	f, sc, ok := rn.part(&block{Header: "--not " + x.arg, Arg: true}, arg)
	if ok {
		x.f, x.lo, x.hi = f, sc.lo, sc.hi
	}
}

// excluded returns the --not that leaves out the occurrence at off in f.
func (rn *renaming) excluded(f *file, off int) *exclusion {
	for _, x := range rn.not {
		switch {
		case x.dir != "":
			if strings.HasPrefix(f.abs, x.dir+string(filepath.Separator)) {
				return x
			}
		case x.f == f && off >= x.lo && off < x.hi:
			return x
		}
	}
	return nil
}

// place reads one place: a folder (every file below it that has the old
// word), a file, or a ref into one.
func (rn *renaming) place(arg string) {
	if fi, err := os.Stat(rn.abs(arg)); err == nil && fi.IsDir() {
		for _, p := range rn.under(rn.abs(arg)) {
			// Only the folder's own files: a link in it may lead anywhere.
			if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				rn.skipped = append(rn.skipped, p)
				continue
			}
			// Most files of a folder don't have the word: they aren't opened.
			if bytes.IndexByte(data, 0) >= 0 || !bytes.Contains(data, []byte(rn.o.old)) {
				continue
			}
			rn.scan(p)
		}
		return
	}
	rn.scan(arg)
}

// scan finds the old word in one file or part of a file.
func (rn *renaming) scan(place string) {
	rn.places++
	f, sc, ok := rn.part(&block{Header: place, Arg: true}, place)
	if !ok {
		return
	}
	scope := f.buf[sc.lo:sc.hi]
	found := wordHits(scope, rn.o.old, rn.o.inside)
	if !rn.o.inside {
		if all := wordHits(scope, rn.o.old, true); len(all) > len(found) {
			if rn.inWords == 0 {
				whole := map[int]bool{}
				for _, o := range found {
					whole[o] = true
				}
				for _, o := range all {
					if !whole[o] {
						rn.inFirst = fmt.Sprintf("%s:%d", f.rel, sc.whole.lineAt(sc.lo+o)+1)
						break
					}
				}
			}
			rn.inWords += len(all) - len(found)
		}
	}
	for _, o := range found {
		off := sc.lo + o
		if x := rn.excluded(f, off); x != nil {
			x.left++
			continue
		}
		if rn.hits[f] == nil {
			rn.hits[f] = map[int]bool{}
		}
		rn.hits[f][off] = true
	}
}

// renamed is what a rename did to one file.
type renamed struct {
	f      *file
	count  int      // occurrences replaced
	lines  []int    // the lines they were on, 1-based, in order
	before []string // those lines as they were
	had    []int    // lines that had the new word already
	check  string
}

// apply replaces the occurrences found in f.
func (rn *renaming) apply(f *file) *renamed {
	offs := make([]int, 0, len(rn.hits[f]))
	for o := range rn.hits[f] {
		offs = append(offs, o)
	}
	sort.Ints(offs)
	whole := newLineIndex(f.buf)
	rd := &renamed{f: f, count: len(offs)}
	for _, o := range wordHits(f.buf, rn.o.new, rn.o.inside) {
		if l := whole.lineAt(o) + 1; len(rd.had) == 0 || rd.had[len(rd.had)-1] != l {
			rd.had = append(rd.had, l)
		}
	}
	var sb strings.Builder
	prev := 0
	for _, o := range offs {
		if l := whole.lineAt(o) + 1; len(rd.lines) == 0 || rd.lines[len(rd.lines)-1] != l {
			rd.lines = append(rd.lines, l)
			rd.before = append(rd.before, whole.line(l-1))
		}
		sb.WriteString(f.buf[prev:o])
		sb.WriteString(rn.o.new)
		prev = o + len(rn.o.old)
	}
	sb.WriteString(f.buf[prev:])
	before := f.buf
	f.buf = sb.String()
	rd.check = check(f.rel, before, f.buf, true)
	return rd
}

// around is a line for the report: without its indentation, and when it is
// long, the part around the first word.
func around(line, word string) string {
	const width = 140
	line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	if utf8.RuneCountInString(line) <= width {
		return line
	}
	rs := []rune(line)
	at := utf8.RuneCountInString(line[:max(strings.Index(line, word), 0)])
	lo := max(0, min(at-40, len(rs)-width))
	hi := min(len(rs), lo+width)
	out := string(rs[lo:hi])
	if lo > 0 {
		out = "…" + out
	}
	if hi < len(rs) {
		out += "…"
	}
	return out
}

var seekable = regexp.MustCompile(`^[\pL\pN_.]+$`)

// rename runs splice --rename.
func rename(o options, out *bufio.Writer, p painter) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(out, "splice: %v\n", err)
		return 2
	}
	info, err := repo.Detect(cwd)
	if err != nil {
		fmt.Fprintf(out, "splice: %v\n", err)
		return 2
	}
	rn := &renaming{o: o, s: newSession(info, cwd), hits: map[*file]map[int]bool{}}
	s := rn.s
	for _, n := range o.not {
		rn.exclude(n)
	}
	for _, pl := range o.inputs {
		rn.place(pl)
	}
	what := fmt.Sprintf("%s → %s", o.old, o.new)
	if len(s.failed) > 0 {
		fmt.Fprintf(out, "%s\n", p.fail(fmt.Sprintf("splice: %s: %d of %s failed, nothing written", what, len(s.failed), plural(rn.places+len(o.not), "place"))))
		for _, fl := range s.failed {
			fmt.Fprintf(out, "%s: %s\n", p.head(fl.b.where()), fl.msg)
			for _, d := range fl.detail {
				fmt.Fprintf(out, "  %s\n", d)
			}
		}
		return 1
	}
	notes := func() {
		for _, x := range rn.not {
			if x.left > 0 {
				fmt.Fprintf(out, "%s\n", p.dim(fmt.Sprintf("note: --not %s left out %s", x.arg, times(x.left))))
			} else {
				fmt.Fprintf(out, "%s\n", p.dim(fmt.Sprintf("note: --not %s left nothing out: %s isn't there", x.arg, o.old)))
			}
		}
		if n := len(rn.skipped); n > 0 {
			fmt.Fprintf(out, "%s\n", p.dim(fmt.Sprintf("note: %s couldn't be read and %s skipped, the first: %s", plural(n, "file"), wasWere(n), rn.skipped[0])))
		}
		if rn.inWords > 0 {
			fmt.Fprintf(out, "%s\n", p.dim(fmt.Sprintf("note: inside longer words %s stays, %s, the first at %s; --inside-words renames those too", o.old, times(rn.inWords), rn.inFirst)))
		}
	}

	var changed []*file
	for _, f := range s.order {
		if len(rn.hits[f]) > 0 {
			changed = append(changed, f)
		}
	}
	if len(changed) == 0 {
		how := " as a word of its own"
		if o.inside {
			how = ""
		}
		fmt.Fprintf(out, "%s\n", p.fail(fmt.Sprintf("splice: nothing renamed: %s isn't in %s%s", o.old, strings.Join(o.inputs, ", "), how)))
		notes()
		return 1
	}
	var done []*renamed
	total, lines := 0, 0
	for _, f := range changed {
		rd := rn.apply(f)
		done = append(done, rd)
		total += rd.count
		lines += len(rd.lines)
	}

	var written []*file
	var werr error
	if !o.dry {
		written, werr = s.write()
	}
	sum := fmt.Sprintf("splice: %s: %s on %s in %s", what, times(total), plural(lines, "line"), plural(len(changed), "file"))
	switch {
	case o.dry:
		fmt.Fprintf(out, "%s, checked, nothing written\n", sum)
	case werr != nil && len(written) == 0:
		fmt.Fprintf(out, "%s\n", p.fail(fmt.Sprintf("splice: nothing written: %v", werr)))
		return 1
	case werr != nil:
		fmt.Fprintf(out, "%s\n", p.fail(fmt.Sprintf("%s; wrote %d of %s, then: %v", sum, len(written), plural(len(changed), "file"), werr)))
	default:
		fmt.Fprintf(out, "%s, written\n", sum)
	}

	list := o.diff || lines <= renameList
	for _, rd := range done {
		f := rd.f
		fmt.Fprintln(out, p.head(f.rel))
		after := splitLines(f.buf)
		ts := f.targets()
		own, keys := owners(after, ts, f.rel), keyed(ts)
		width := len(fmt.Sprint(rd.lines[len(rd.lines)-1]))
		for i := 0; i < len(rd.lines); {
			o := own[rd.lines[i]-1]
			j := i
			for j < len(rd.lines) && own[rd.lines[j]-1] == o {
				j++
			}
			head := "outside definitions"
			if o >= 0 {
				head = fmt.Sprintf("%s %s %s", ts[o].Kind, keys[o], lineRange(ts[o].Line, ts[o].End))
			}
			if !list {
				head += " " + p.dim("("+plural(j-i, "line")+")")
			}
			fmt.Fprintf(out, "  %s\n", head)
			for ; i < j; i++ {
				if !list {
					continue
				}
				l := rd.lines[i]
				if rn.o.diff {
					fmt.Fprintf(out, "    %*d  %s\n", width, l, p.verb("removed", "- "+around(rd.before[i], rn.o.old)))
					fmt.Fprintf(out, "    %*s  %s\n", width, "", p.verb("added", "+ "+around(after[l-1], rn.o.new)))
					continue
				}
				fmt.Fprintf(out, "    %*d  %s\n", width, l, around(after[l-1], rn.o.new))
			}
		}
		if n := len(rd.had); n > 0 {
			at := make([]string, 0, 3)
			for _, l := range rd.had[:min(n, 3)] {
				at = append(at, fmt.Sprintf("L%d", l))
			}
			more := ""
			if n > 3 {
				more = ", …"
			}
			fmt.Fprintf(out, "  %s\n", p.dim(fmt.Sprintf("note: %s was here already, on %s (%s%s): the two are one name now", rn.o.new, plural(n, "line"), strings.Join(at, ", "), more)))
		}
		for _, n := range f.notes {
			fmt.Fprintf(out, "  %s\n", p.dim("note: "+n))
		}
		if rd.check != "" {
			fmt.Fprintf(out, "  %s\n", p.fail("check: "+rd.check+" (written anyway)"))
		}
	}
	if !list {
		fmt.Fprintf(out, "%s\n", p.dim(fmt.Sprintf("note: %d changed lines are more than the %d that get listed; --diff lists every one", lines, renameList)))
	}
	notes()
	if seekable.MatchString(o.old) {
		fmt.Fprintln(out, p.dim(fmt.Sprintf("next: seek %s   what still has the old name, in files not named here", o.old)))
	}
	if werr != nil {
		var left []string
		wrote := map[*file]bool{}
		for _, f := range written {
			wrote[f] = true
		}
		for _, f := range changed {
			if !wrote[f] {
				left = append(left, f.rel)
			}
		}
		fmt.Fprintf(out, "not written: %s\n", strings.Join(left, ", "))
		return 1
	}
	return 0
}

// times is "once", "twice" or "N times".
func times(n int) string {
	switch n {
	case 1:
		return "once"
	case 2:
		return "twice"
	}
	return fmt.Sprintf("%d times", n)
}

func wasWere(n int) string {
	if n == 1 {
		return "was"
	}
	return "were"
}
