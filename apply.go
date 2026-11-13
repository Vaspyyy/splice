package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Vaspyyy/prism/outline"
	"github.com/Vaspyyy/prism/ref"
	"github.com/Vaspyyy/prism/repo"
)

// file is one file the blocks edit, as splice holds it in memory.
type file struct {
	rel     string // as printed: repo-relative, or absolute outside the repository
	abs     string // where it's read and written (symlinks resolved)
	exists  bool   // it existed before splice ran
	orig    []byte // its content as read
	crlf    bool   // every line ends in \r\n: worked on as \n, written back as \r\n
	buf     string // the current content, with \n line endings
	mode    fs.FileMode
	info    fs.FileInfo // what Stat said when it was read, to spot hard links
	created bool        // a block created it
	edits   []*edit     // the blocks applied to it, in order
	notes   []string    // what to say about it (how blocks matched, …)
}

// edit is one applied block, for the report.
type edit struct {
	b        *block
	old, new string // the text replaced and the text put there (\n line endings)
	line     int    // 1-based line in the buffer where the change starts, at the time
	count    int    // occurrences replaced (SEARCH all)
}

// failure is a block that can't be applied: what's wrong, and lines that
// help fix it.
type failure struct {
	b      *block
	msg    string
	detail []string
}

// session is one run: the repository, the files and what happened to them.
type session struct {
	info   *repo.Info
	cwd    string
	files  map[string]*file // by abs
	order  []*file
	failed []*failure
	listed []string // the files git sees, for path suffixes; loaded once
	loaded bool
}

func newSession(info *repo.Info, cwd string) *session {
	return &session{info: info, cwd: cwd, files: map[string]*file{}}
}

func (s *session) fail(b *block, msg string, detail ...string) {
	s.failed = append(s.failed, &failure{b: b, msg: msg, detail: detail})
}

// gitFiles lists the files git sees, tracked and untracked, not ignored.
func (s *session) gitFiles() []string {
	if !s.loaded {
		s.loaded = true
		if s.info.Git {
			cmd := exec.Command("git", "-C", s.info.Root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
			if out, err := cmd.Output(); err == nil {
				for _, f := range strings.Split(string(out), "\x00") {
					if f != "" {
						s.listed = append(s.listed, f)
					}
				}
			}
		}
	}
	return s.listed
}

// apply runs one block against the files in memory.
func (s *session) apply(b *block) {
	r := ref.Parse(b.Header, s.info.IsRev)
	if r.Rev != "" {
		s.fail(b, fmt.Sprintf("the header names %s as of %s; splice edits the files as they are now: leave out @%s", r.Path, r.Rev, r.Rev))
		return
	}
	creating := !r.HasTarget && !b.HasOld
	f, msg, detail := s.open(r.Path, creating)
	if msg != "" {
		s.fail(b, msg, detail...)
		return
	}
	if creating {
		if f.exists || f.created {
			s.fail(b, fmt.Sprintf("the old text is empty, which creates a file, but %s exists: put the text to replace in the block, or name a definition to replace it whole (%s:Name)", f.rel, f.rel))
			return
		}
		if b.All {
			s.fail(b, `"SEARCH all" needs old text`)
			return
		}
		f.buf, f.created = b.New, true
		if b.HasNew {
			f.buf += "\n"
		}
		f.edits = append(f.edits, &edit{b: b, new: b.New, line: 1})
		return
	}
	if !f.exists && !f.created {
		s.fail(b, fmt.Sprintf("no file %s", f.rel), s.similarFiles(r.Path)...)
		return
	}

	// the scope: the whole file, a definition or some lines
	lo, hi := 0, len(f.buf)
	scopeName := ""
	targets := ref.Targets(f.rel, outline.Parse(f.rel, []byte(f.buf), outline.MaxSize))
	whole := newLineIndex(f.buf)
	lineSpan := func(first, last int) (int, int) { // 1-based lines, inclusive
		first = max(first, 1)
		last = min(last, whole.count())
		if first > last {
			return 0, 0
		}
		end := whole.lineEnd(last - 1)
		if end < len(f.buf) {
			end++ // the scope includes the last line's newline
		}
		return whole.starts[first-1], end
	}
	var def *ref.Target
	switch {
	case !r.HasTarget:
	case r.End > 0:
		if r.End > whole.count() || r.Line < 1 || r.End < r.Line {
			s.fail(b, fmt.Sprintf("%s has %s; the header's lines %d-%d aren't all there", f.rel, plural(whole.count(), "line"), r.Line, r.End))
			return
		}
		lo, hi = lineSpan(r.Line, r.End)
		scopeName = fmt.Sprintf("%s:%d-%d", f.rel, r.Line, r.End)
	case r.Name == "" && r.Line < 1 || lineLike(r.Name):
		s.fail(b, fmt.Sprintf("%q isn't a line or a range in %s: lines start at 1 (path:104, path:80-120)", b.Header, f.rel))
		return
	default:
		idx := ref.Match(r, targets, []string{f.rel})
		switch {
		case len(idx) == 0 && r.Line > 0:
			if r.Line > whole.count() {
				s.fail(b, fmt.Sprintf("%s has %s; the header's line %d isn't there", f.rel, plural(whole.count(), "line"), r.Line))
				return
			}
			f.note(fmt.Sprintf("block %d: line %d is outside any definition, so the whole file was searched", b.N, r.Line))
		case len(idx) == 0:
			s.fail(b, fmt.Sprintf("%s has no definition %s", f.rel, r.Name), s.likelyNames(r, targets, f.rel)...)
			return
		case len(idx) > 1:
			var refs []string
			for _, i := range idx {
				refs = append(refs, refOf(targets, i))
			}
			s.fail(b, fmt.Sprintf("%q names %d definitions; use one of these as the header:", b.Header, len(idx)), refs...)
			return
		default:
			t := targets[idx[0]]
			def = &t
			lo, hi = lineSpan(t.Line, t.End)
			scopeName = refOf(targets, idx[0])
		}
	}

	if !b.HasOld { // replace the whole definition
		if def == nil {
			s.fail(b, fmt.Sprintf("the old text is empty: that replaces a whole definition, but %q names lines, not a definition", b.Header))
			return
		}
		if b.All {
			s.fail(b, `"SEARCH all" needs old text`)
			return
		}
		if !b.HasNew || blank(b.New) {
			s.fail(b, "the old text is empty, which replaces the whole definition, but so is the new text: deleting a definition this way isn't supported yet (it would leave its doc comment behind); put the definition and its doc comment in the old text instead")
			return
		}
		last := def.End // without the blank lines a section ends in, which stay
		for last > def.Line && blank(whole.line(last-1)) {
			last--
		}
		end := whole.lineEnd(last - 1)
		text := b.New
		oldFirst := whole.line(def.Line - 1)
		nl := strings.Split(b.New, "\n")
		k := 0
		for k < len(nl) && blank(nl[k]) {
			k++
		}
		if k < len(nl) {
			want, have := indentOf(oldFirst), indentOf(nl[k])
			if want != have {
				var prefix string
				var add bool
				switch {
				case strings.HasPrefix(want, have):
					prefix, add = want[len(have):], true
				case strings.HasPrefix(have, want):
					prefix = have[len(want):]
				}
				if prefix != "" {
					if t, err := reindent(b.New, prefix, add); err == nil {
						text = t
						f.note(fmt.Sprintf("block %d: the new definition was moved %s, to where %s starts", b.N, describeShift(prefix, add), def.Name))
					}
				}
			}
		}
		s.replace(f, b, span{start: lo, end: end, text: text}, 1)
		return
	}

	if blank(b.Old) {
		s.fail(b, "the old text is only blank lines, which would match in too many places; add a line of text next to them")
		return
	}
	scope := f.buf[lo:hi]
	if b.All {
		offs := aligned(scope, b.Old, exactAll(scope, b.Old))
		if len(offs) == 0 {
			s.notFound(f, b, scope, lo, scopeName, targets)
			return
		}
		if overlap(offs, len(b.Old)) {
			s.fail(b, fmt.Sprintf("the old text occurs %d times%s, and some of them overlap, so which to replace is unclear; make it longer", len(offs), inScope(scopeName)))
			return
		}
		for i := len(offs) - 1; i >= 0; i-- { // from the end, so the offsets before stay right
			start, end := lo+offs[i], lo+offs[i]+len(b.Old)
			if !b.HasNew {
				start, end = withLineBreak(f.buf, start, end)
			}
			f.shift(strings.Count(f.buf[:start], "\n")+1, f.buf[start:end], b.New)
			f.buf = f.buf[:start] + b.New + f.buf[end:]
		}
		f.edits = append(f.edits, &edit{b: b, old: b.Old, new: b.New, line: whole.lineAt(lo+offs[0]) + 1, count: len(offs)})
		if b.Old == b.New {
			f.note(fmt.Sprintf("block %d: the new text is the same as the old", b.N))
		}
		return
	}
	offs := aligned(scope, b.Old, exactAll(scope, b.Old))
	var spans []span
	for _, o := range offs {
		spans = append(spans, span{start: o, end: o + len(b.Old), text: b.New})
	}
	if len(spans) == 0 {
		var err error
		spans, err = tolerant(newLineIndex(scope), b.Old, b.New)
		if err != nil {
			s.fail(b, err.Error())
			return
		}
	}
	switch len(spans) {
	case 0:
		s.notFound(f, b, scope, lo, scopeName, targets)
	case 1:
		sp := spans[0]
		sp.start += lo
		sp.end += lo
		if sp.note != "" {
			f.note(fmt.Sprintf("block %d: %s", b.N, sp.note))
		}
		s.replace(f, b, sp, 1)
	default:
		var where []string
		perLine := map[int]int{}
		for _, sp := range spans {
			perLine[whole.lineAt(lo+sp.start)]++
		}
		for i, sp := range spans {
			if i == 10 {
				where = append(where, fmt.Sprintf("… and %d more", len(spans)-10))
				break
			}
			l := whole.lineAt(lo + sp.start)
			at := s.locate(f, targets, l+1)
			if perLine[l] > 1 { // several on one line: path:line:col
				col := lo + sp.start - whole.starts[l] + 1
				at = strings.Replace(at, fmt.Sprintf(":%d", l+1), fmt.Sprintf(":%d:%d", l+1, col), 1)
			}
			where = append(where, at)
		}
		how := `make it unique with more lines, or use "SEARCH all" to replace every one`
		if def == nil && r.End == 0 {
			if in := s.containing(f, targets, spans, lo, whole); in != "" {
				how = fmt.Sprintf(`make it unique with more lines or a header like %s, or use "SEARCH all" to replace every one`, in)
			}
		}
		where = append(where, how)
		what := "the old text"
		if spans[0].note != "" {
			what = "the old text (" + spans[0].note + ")"
		}
		s.fail(b, fmt.Sprintf("%s occurs %d times%s", what, len(spans), inScope(scopeName)), where...)
	}
}

func inScope(name string) string {
	if name == "" {
		return ""
	}
	return " in " + name
}

// replace puts sp into f's buffer and records the edit.
func (s *session) replace(f *file, b *block, sp span, count int) {
	if !b.HasNew {
		sp.start, sp.end = withLineBreak(f.buf, sp.start, sp.end)
	}
	line := strings.Count(f.buf[:sp.start], "\n") + 1
	old := f.buf[sp.start:sp.end]
	f.shift(line, old, sp.text)
	f.buf = f.buf[:sp.start] + sp.text + f.buf[sp.end:]
	f.edits = append(f.edits, &edit{b: b, old: old, new: sp.text, line: line, count: count})
	if old == sp.text {
		f.note(fmt.Sprintf("block %d: the new text is the same as the old", b.N))
	}
}

// withLineBreak extends a match of whole lines over the line break after
// it, so that a block with no new lines deletes the lines instead of
// leaving them empty. A match inside a line stays as it is.
func withLineBreak(buf string, start, end int) (int, int) {
	if start > 0 && buf[start-1] != '\n' {
		return start, end // starts inside a line
	}
	switch {
	case end < len(buf) && buf[end] == '\n':
		return start, end + 1
	case end == len(buf) && end > start && buf[end-1] != '\n' && start > 0:
		// the last line of a file without a final newline: the line break
		// before it goes, so the file still doesn't end in one
		return start - 1, end
	}
	return start, end
}

// lineLike is a header target that looks like a line or range but isn't
// one prism reads as such ("0", "0-3").
func lineLike(name string) bool {
	if name == "" {
		return false
	}
	return strings.Trim(name, "0123456789-:") == ""
}

func (f *file) note(n string) { f.notes = append(f.notes, n) }

// shift moves the lines of earlier edits below line by what replacing old
// with new there adds or removes, so --diff shows lines of the final file.
func (f *file) shift(line int, old, new string) {
	d := strings.Count(new, "\n") - strings.Count(old, "\n")
	for _, e := range f.edits {
		if e.line > line {
			e.line += d
		}
	}
}

// notFound reports a block whose old text isn't in its scope, with the
// nearest place, or the new text when it's there already.
func (s *session) notFound(f *file, b *block, scope string, lo int, scopeName string, targets []ref.Target) {
	in := scopeName
	if in == "" {
		in = f.rel
	}
	head := "the old text isn't in " + in
	whole := newLineIndex(f.buf)
	var detail []string
	if t := strings.TrimSpace(b.New); len(t) >= 12 || strings.Contains(t, "\n") {
		if i := strings.Index(scope, b.New); i >= 0 {
			detail = append(detail, fmt.Sprintf("but the new text is, at %s: was this block applied already?", s.locate(f, targets, whole.lineAt(lo+i)+1)))
		}
	}
	sx := newLineIndex(scope)
	nr, ok := nearest(sx, b.Old)
	if !ok {
		if len(detail) == 0 {
			detail = append(detail, "nothing there looks like it")
		}
		s.fail(b, head, detail...)
		return
	}
	base := whole.lineAt(lo) // 0-based line where the scope starts
	first, last := base+nr.first+1, base+nr.last+1
	place := fmt.Sprintf("%s:%d", f.rel, first)
	if last > first {
		place = fmt.Sprintf("%s:%d-%d", f.rel, first, last)
	}
	if in := s.innermost(f, targets, first); in != "" {
		place += " in " + in
	}
	detail = append(detail, "nearest: "+place)
	if nr.at >= 0 {
		why := ""
		if strings.TrimSpace(nr.file) == strings.TrimSpace(nr.block) {
			why = " (only in its indentation)"
		}
		detail = append(detail,
			fmt.Sprintf("the %s line of the old text differs%s:", ordinal(nr.at+1), why),
			"  file:  "+clip(show(nr.file), 120),
			"  block: "+clip(show(nr.block), 120))
	}
	s.fail(b, head, detail...)
}

// show makes whitespace that matters visible in a quoted line.
func show(s string) string {
	s = strings.ReplaceAll(s, "\t", "→")
	if t := strings.TrimRight(s, " "); len(t) < len(s) {
		s = t + strings.Repeat("·", len(s)-len(t))
	}
	return s
}

// innermost names the definition containing a 1-based line, or "".
func (s *session) innermost(f *file, targets []ref.Target, line int) string {
	if i := ref.Innermost(targets, f.rel, line); i >= 0 {
		return strings.TrimPrefix(refOf(targets, i), f.rel+":")
	}
	return ""
}

// locate is "path:line in Name" for a 1-based line.
func (s *session) locate(f *file, targets []ref.Target, line int) string {
	at := fmt.Sprintf("%s:%d", f.rel, line)
	if in := s.innermost(f, targets, line); in != "" {
		at += " in " + in
	}
	return at
}

// containing suggests a ref header for the first of several matches, when
// they're in different definitions and the first one's definition holds
// only one of them.
func (s *session) containing(f *file, targets []ref.Target, spans []span, lo int, whole *lineIndex) string {
	count := map[int]int{}
	for _, sp := range spans {
		count[ref.Innermost(targets, f.rel, whole.lineAt(lo+sp.start)+1)]++
	}
	i := ref.Innermost(targets, f.rel, whole.lineAt(lo+spans[0].start)+1)
	if i < 0 || count[i] != 1 {
		return ""
	}
	return refOf(targets, i)
}

// refOf formats target i as a ref, with an ordinal when its name repeats.
func refOf(ts []ref.Target, i int) string {
	ords := ref.Ordinals(ts)
	return ref.Symbol(ts[i].Path, ts[i].Name, ords[i]).String()
}

// likelyNames lists definitions of the file a name might have meant.
func (s *session) likelyNames(r ref.Ref, targets []ref.Target, rel string) []string {
	want := strings.ToLower(r.Name)
	last := want
	if i := strings.LastIndexAny(want, ".:"); i >= 0 {
		last = want[i+1:]
	}
	type cand struct {
		ref   string
		score float64
	}
	var cs []cand
	for i, t := range targets {
		n := strings.ToLower(t.Name)
		l := n
		if j := strings.LastIndexAny(n, ".:"); j >= 0 {
			l = n[j+1:]
		}
		sc := max(dice(want, n), dice(last, l))
		if strings.Contains(n, last) || strings.Contains(last, l) {
			sc += 0.5
		}
		if sc >= 0.5 {
			cs = append(cs, cand{refOf(targets, i), sc})
		}
	}
	sort.SliceStable(cs, func(a, b int) bool { return cs[a].score > cs[b].score })
	var out []string
	for i, c := range cs {
		if i == 5 {
			break
		}
		out = append(out, c.ref)
	}
	if len(out) == 0 {
		return []string{fmt.Sprintf("%s has %s; seek %s lists them", rel, plural(len(targets), "definition"), rel)}
	}
	return append([]string{"did you mean:"}, out...)
}

// similarFiles lists files git sees whose names look like p.
func (s *session) similarFiles(p string) []string {
	base := strings.ToLower(filepath.Base(p))
	var out []string
	for _, f := range s.gitFiles() {
		if strings.ToLower(filepath.Base(f)) == base {
			out = append(out, f)
			if len(out) == 5 {
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return append([]string{"files with that name:"}, out...)
}

// open finds (or loads) the file a header's path names. creating says the
// block would create it, so it may not exist.
func (s *session) open(p string, creating bool) (*file, string, []string) {
	abs, rel, msg, detail := s.where(p, creating)
	if msg != "" {
		return nil, msg, detail
	}
	if f := s.files[abs]; f != nil {
		return f, "", nil
	}
	f := &file{rel: rel, abs: abs}
	fi, err := os.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// a new file: through the real folder, so two spellings of one
		// path are one file
		if d, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
			f.abs = filepath.Join(d, filepath.Base(abs))
		}
	case err != nil:
		return nil, err.Error(), nil
	default:
		// the real path, through symlinks anywhere in it, so that one file
		// named two ways is edited as one
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			if creating {
				return nil, fmt.Sprintf("%s is a symlink to a missing file; splice doesn't create files through symlinks", rel), nil
			}
			return nil, fmt.Sprintf("%s is a broken symlink", rel), nil
		}
		if fi, err = os.Stat(real); err != nil {
			return nil, err.Error(), nil
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Sprintf("%s isn't a regular file", rel), nil
		}
		f.abs, f.info = real, fi
		for _, g := range s.order { // hard links
			if g.info != nil && os.SameFile(g.info, fi) {
				s.files[abs] = g
				return g, "", nil
			}
		}
		data, err := os.ReadFile(f.abs)
		if err != nil {
			return nil, err.Error(), nil
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return nil, fmt.Sprintf("%s is a binary file", rel), nil
		}
		f.exists, f.orig = true, data
		f.mode = fi.Mode() & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
		text := string(data)
		if n := strings.Count(text, "\n"); n > 0 && strings.Count(text, "\r\n") == n {
			f.crlf = true
			text = strings.ReplaceAll(text, "\r\n", "\n")
		}
		f.buf = text
	}
	if g := s.files[f.abs]; g != nil { // the same new file, spelled another way
		s.files[abs] = g
		return g, "", nil
	}
	s.files[abs] = f
	s.files[f.abs] = f
	s.order = append(s.order, f)
	return f, "", nil
}

// where turns a header's path into the file's absolute and printed paths.
func (s *session) where(p string, creating bool) (abs, rel, msg string, detail []string) {
	if p == "" {
		return "", "", "the header has no path", nil
	}
	res, err := s.info.Resolve(p, s.cwd)
	if errors.Is(err, repo.ErrOutside) {
		a := p
		if !filepath.IsAbs(a) {
			a = filepath.Join(s.cwd, a)
		}
		a = filepath.Clean(a)
		return a, a, "", nil
	}
	if err != nil {
		return "", "", err.Error(), nil
	}
	if res.Exists {
		return s.info.Abs(res.Rel), res.Rel, "", nil
	}
	if creating {
		// a new file: of the readings whose folder exists, the first
		// (relative to the root, as tools print paths) wins
		rs := s.info.Readings(p, s.cwd)
		for _, r := range rs {
			if d, err := os.Stat(filepath.Dir(s.info.Abs(r))); err == nil && d.IsDir() {
				return s.info.Abs(r), r, "", nil
			}
		}
		return s.info.Abs(res.Rel), res.Rel, "", nil
	}
	// an existing file named by the end of its path ("pty.rs")
	if m := s.info.Match(p, s.cwd, s.gitFiles()); len(m) == 1 {
		return s.info.Abs(m[0]), m[0], "", nil
	} else if len(m) > 1 {
		if len(m) > 8 {
			m = append(m[:8], fmt.Sprintf("… and %d more", len(m)-8))
		}
		return "", "", fmt.Sprintf("%s could be %d files; give more of its path:", p, len(m)), m
	}
	return s.info.Abs(res.Rel), res.Rel, "", nil
}

// write saves every changed file: each to a temporary file in its folder,
// then all of them renamed into place. If a file changed on disk since it
// was read, or a temporary file can't be written, nothing is replaced.
func (s *session) write() (written []*file, err error) {
	type staged struct {
		f   *file
		tmp string
	}
	var st []staged
	drop := func(from int) {
		for _, x := range st[from:] {
			os.Remove(x.tmp)
		}
	}
	// first every file to a temporary file next to it, so that a file that
	// changed on disk or a folder that can't be written stops everything
	// before anything is replaced
	for _, f := range s.order {
		if !f.changed() {
			continue
		}
		if f.exists {
			now, rerr := os.ReadFile(f.abs)
			if rerr != nil || !bytes.Equal(now, f.orig) {
				drop(0)
				return nil, fmt.Errorf("%s changed on disk while splice ran", f.rel)
			}
		} else if _, serr := os.Lstat(f.abs); serr == nil {
			drop(0)
			return nil, fmt.Errorf("%s was created by something else while splice ran", f.rel)
		}
		tmp, err := stage(f)
		if err != nil {
			drop(0)
			return nil, fmt.Errorf("%s: %v", f.rel, err)
		}
		st = append(st, staged{f, tmp})
	}
	// then the renames, which only fail in odd cases (a folder that changed)
	for i, x := range st {
		if err := os.Rename(x.tmp, x.f.abs); err != nil {
			drop(i)
			return written, fmt.Errorf("%s: %v", x.f.rel, err)
		}
		written = append(written, x.f)
	}
	return written, nil
}

// changed tells whether f's content differs from what was read.
func (f *file) changed() bool {
	return f.created || f.exists && f.out() != string(f.orig)
}

// out is the content to write, with the file's line endings.
func (f *file) out() string {
	if f.crlf {
		return strings.ReplaceAll(f.buf, "\n", "\r\n")
	}
	return f.buf
}

// stage writes f's new content to a temporary file next to it, with its
// mode, and returns its path.
func stage(f *file) (string, error) {
	dir := filepath.Dir(f.abs)
	if f.created {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(f.abs)+".splice-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	fail := func(err error) (string, error) {
		tmp.Close()
		os.Remove(name)
		return "", err
	}
	if _, err := tmp.WriteString(f.out()); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	mode := f.mode
	if f.created {
		mode = 0o644
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}
