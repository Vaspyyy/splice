package main

// A case for each defect the first reviews of splice 0.1.0 found (Opus 5.5,
// Sonnet 5.5 and GPT-6.1 Terra, 2026-11-13).

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vaspyyy/prism/ref"
)

func TestBlankOldText(t *testing.T) {
	for _, in := range []string{
		"a.txt\n<<<<<<< SEARCH all\n\n=======\nX\n>>>>>>> REPLACE\n",
		"a.txt\n<<<<<<< SEARCH\n\n=======\nX\n>>>>>>> REPLACE\n",
		"a.txt\n<<<<<<< SEARCH\n  \n\n=======\nX\n>>>>>>> REPLACE\n",
	} {
		dir := tree(t, map[string]string{"a.txt": "ab\n\ncd\n"})
		out, code := splice(t, dir, in)
		if code != 1 || !strings.Contains(out, "only blank lines") {
			t.Errorf("exit %d:\n%s", code, out)
		}
		if got := read(t, dir, "a.txt"); got != "ab\n\ncd\n" {
			t.Errorf("a.txt = %q", got)
		}
	}
}

func TestSecondDivider(t *testing.T) {
	dir := tree(t, map[string]string{"doc.md": "Title\n=======\n\nold para\n"})
	out, code := splice(t, dir, "doc.md\n<<<<<<< SEARCH\nTitle\n=======\n\nold para\n=======\nTitle\n=======\n\nnew para\n>>>>>>> REPLACE\n")
	if code != 2 || !strings.Contains(out, "a second =======") || !strings.Contains(out, "<<<<<<<<< SEARCH") {
		t.Errorf("exit %d:\n%s", code, out)
	}
	if got := read(t, dir, "doc.md"); got != "Title\n=======\n\nold para\n" {
		t.Errorf("doc.md = %q", got)
	}
}

func TestOverlapping(t *testing.T) {
	dir := tree(t, map[string]string{"o.txt": "x\nx\nx\n", "a.txt": "aaa\n"})
	out, code := splice(t, dir, "o.txt\n<<<<<<< SEARCH\nx\nx\n=======\nY\n>>>>>>> REPLACE\na.txt\n<<<<<<< SEARCH\naa\n=======\nX\n>>>>>>> REPLACE\n")
	if code != 1 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "block 1 (o.txt, line 2): the old text occurs 2 times", "block 2 (a.txt, line 9): the old text occurs 2 times", "a.txt:1:1\n", "a.txt:1:2\n")
	out, code = splice(t, dir, "a.txt\n<<<<<<< SEARCH all\naa\n=======\nX\n>>>>>>> REPLACE\n")
	if code != 1 || !strings.Contains(out, "overlap") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestOneFileTwoPaths(t *testing.T) {
	dir := tree(t, map[string]string{"real/f.txt": "one\ntwo\n"})
	if err := os.Symlink("real", filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, "real/f.txt"), filepath.Join(dir, "hard.txt")); err != nil {
		t.Fatal(err)
	}
	out, code := splice(t, dir, `real/f.txt
<<<<<<< SEARCH
one
=======
ONE
>>>>>>> REPLACE
alias/f.txt
<<<<<<< SEARCH
ONE
two
=======
ONE
TWO
>>>>>>> REPLACE
hard.txt
<<<<<<< SEARCH
TWO
=======
2
>>>>>>> REPLACE
`)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "3 blocks in 1 file, written")
	if got := read(t, dir, "real/f.txt"); got != "ONE\n2\n" {
		t.Errorf("real/f.txt = %q", got)
	}
}

// TestHardLinkInPlace checks that a file with another hard link is written
// in place, so both names show the edit (GPT-6.1 Terra's re-check).
func TestHardLinkInPlace(t *testing.T) {
	dir := tree(t, map[string]string{"a": "one\ntwo\n"})
	if err := os.Link(filepath.Join(dir, "a"), filepath.Join(dir, "b")); err != nil {
		t.Fatal(err)
	}
	if out, code := splice(t, dir, "b\n<<<<<<< SEARCH\none\n=======\nONE\n>>>>>>> REPLACE\n"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := read(t, dir, "a"); got != "ONE\ntwo\n" {
		t.Errorf("a = %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".b.splice-*")); len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestNothingWrittenWhenAFolderIsReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to a read-only folder")
	}
	dir := tree(t, map[string]string{"a.txt": "a\n", "ro/b.txt": "b\n"})
	ro := filepath.Join(dir, "ro")
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o755) })
	out, code := splice(t, dir, "a.txt\n<<<<<<< SEARCH\na\n=======\nA\n>>>>>>> REPLACE\nro/b.txt\n<<<<<<< SEARCH\nb\n=======\nB\n>>>>>>> REPLACE\n")
	if code != 1 || !strings.Contains(out, "splice: nothing written: ro/b.txt:") {
		t.Errorf("exit %d:\n%s", code, out)
	}
	if got := read(t, dir, "a.txt"); got != "a\n" {
		t.Errorf("a.txt was written: %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".a.txt.splice-*")); len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestAppliedAlreadyKeepsNearest(t *testing.T) {
	dir := tree(t, map[string]string{"b.go": "package b\n\nfunc f() error {\n\treturn nil\n}\n"})
	out, _ := splice(t, dir, "b.go\n<<<<<<< SEARCH\n\treturn nill\n}\n=======\n\treturn nil\n}\n>>>>>>> REPLACE\n")
	wants(t, out, "was this block applied already?", "nearest: b.go:4-5 in f", "block: →return nill")
}

func TestSectionKeepsBlankLine(t *testing.T) {
	dir := tree(t, map[string]string{"R.md": "# R\n\n## Install\n\nold text\n\n## Usage\n\nuse it\n"})
	out, code := splice(t, dir, "R.md:Install\n<<<<<<< SEARCH\n=======\n## Install\n\nnew install text\n>>>>>>> REPLACE\n")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := read(t, dir, "R.md"); got != "# R\n\n## Install\n\nnew install text\n\n## Usage\n\nuse it\n" {
		t.Errorf("R.md = %q", got)
	}
}

func TestDiffLinesAreFinal(t *testing.T) {
	dir := tree(t, map[string]string{"a.txt": "1\n2\n3\n4\n"})
	out, code := splice(t, dir, "a.txt\n<<<<<<< SEARCH\n4\n=======\nfour\n>>>>>>> REPLACE\n<<<<<<< SEARCH\n1\n=======\n1\n1a\n1b\n>>>>>>> REPLACE\n", "--diff")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "block 1 at L6:", "block 2 at L1:")

	// a later block that takes in an earlier one's lines: the earlier one
	// starts where the later one does, never at line 0
	dir = tree(t, map[string]string{"a.txt": "one\ntwo\nthree\nfour\n"})
	out, _ = splice(t, dir, "a.txt\n<<<<<<< SEARCH\ntwo\n=======\nTWO\n>>>>>>> REPLACE\n<<<<<<< SEARCH\none\nTWO\nthree\n=======\nX\n>>>>>>> REPLACE\n", "--diff")
	wants(t, out, "block 1 at L1:", "block 2 at L1:")
}

func TestSpecialModeBits(t *testing.T) {
	dir := tree(t, map[string]string{"s.sh": "x\n"})
	p := filepath.Join(dir, "s.sh")
	if err := os.Chmod(p, 0o755|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode()&os.ModeSetgid == 0 {
		t.Skip("this file system doesn't keep the setgid bit")
	}
	if _, code := splice(t, dir, "s.sh\n<<<<<<< SEARCH\nx\n=======\ny\n>>>>>>> REPLACE\n"); code != 0 {
		t.Fatal("exit", code)
	}
	if fi, _ := os.Stat(p); fi.Mode()&os.ModeSetgid == 0 || fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v", fi.Mode())
	}
}

func TestDeleteLastLineWithoutNewline(t *testing.T) {
	dir := tree(t, map[string]string{"n.txt": "a\nb", "m.txt": "a\nb\n"})
	out, code := splice(t, dir, "n.txt\n<<<<<<< SEARCH\nb\n=======\n>>>>>>> REPLACE\nm.txt\n<<<<<<< SEARCH\nb\n=======\n>>>>>>> REPLACE\n")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := read(t, dir, "n.txt"); got != "a" {
		t.Errorf("n.txt = %q", got)
	}
	if got := read(t, dir, "m.txt"); got != "a\n" {
		t.Errorf("m.txt = %q", got)
	}
}

func TestMixedLineEndings(t *testing.T) {
	dir := tree(t, map[string]string{"m.txt": "a\r\nb\r\nc\n"})
	out, code := splice(t, dir, "m.txt\n<<<<<<< SEARCH\na\nb\n=======\nX\nY\n>>>>>>> REPLACE\n")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, `ignoring \r and spaces`)
	if got := read(t, dir, "m.txt"); got != "X\r\nY\r\nc\n" {
		t.Errorf("m.txt = %q", got)
	}
	dir = tree(t, map[string]string{"m.txt": "a\r\nb\nc\n", "n.txt": "a\nb\r\nc"})
	if out, code := splice(t, dir, "m.txt\n<<<<<<< SEARCH\na\n=======\n>>>>>>> REPLACE\nn.txt\n<<<<<<< SEARCH\nc\n=======\n>>>>>>> REPLACE\n"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := read(t, dir, "m.txt"); got != "b\nc\n" {
		t.Errorf("m.txt = %q", got)
	}
	if got := read(t, dir, "n.txt"); got != "a\nb" {
		t.Errorf("n.txt = %q", got)
	}
}

func TestShiftAmbiguousEvenIfOneCantBeShifted(t *testing.T) {
	dir := tree(t, map[string]string{"a.txt": "foo\n        foo\n"})
	out, code := splice(t, dir, "a.txt\n<<<<<<< SEARCH\n    foo\n=======\n  bar\n>>>>>>> REPLACE\n")
	if code != 1 || !strings.Contains(out, "occurs 2 times") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestLineHeaders(t *testing.T) {
	dir := tree(t, map[string]string{"a.txt": "a\nb\n"})
	for _, c := range []struct{ header, want string }{
		{"a.txt:2-50", "aren't all there"},
		{"a.txt:50", "line 50 isn't there"},
		{"a.txt:0-1", "lines start at 1"},
	} {
		out, code := splice(t, dir, c.header+"\n<<<<<<< SEARCH\nb\n=======\nB\n>>>>>>> REPLACE\n")
		if code != 1 || !strings.Contains(out, c.want) {
			t.Errorf("%s: exit %d:\n%s", c.header, code, out)
		}
	}
}

// The cases below are from the re-checks of c7c713d (Opus 5.5).

func TestDateHeading(t *testing.T) {
	dir := tree(t, map[string]string{"CHANGELOG.md": "# Log\n\n## 2026-11-13\n\nold\n"})
	if out, code := splice(t, dir, "CHANGELOG.md:2026-11-13\n<<<<<<< SEARCH\nold\n=======\nnew\n>>>>>>> REPLACE\n"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := read(t, dir, "CHANGELOG.md"); !strings.Contains(got, "\nnew\n") {
		t.Errorf("CHANGELOG.md = %q", got)
	}
}

func TestSpacesInsideALine(t *testing.T) {
	dir := tree(t, map[string]string{"c.go": "package c\n\nvar x = 1   // one\n"})
	if out, code := splice(t, dir, "c.go\n<<<<<<< SEARCH\n   // one\n=======\n // the first\n>>>>>>> REPLACE\n"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := read(t, dir, "c.go"); got != "package c\n\nvar x = 1 // the first\n" {
		t.Errorf("c.go = %q", got)
	}
}

func TestNewLinesInACRLFLine(t *testing.T) {
	dir := tree(t, map[string]string{"m.txt": "x\na\r\nb\n"})
	if out, code := splice(t, dir, "m.txt\n<<<<<<< SEARCH\na\n=======\na\nb2\n>>>>>>> REPLACE\n"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := read(t, dir, "m.txt"); got != "x\na\r\nb2\r\nb\n" {
		t.Errorf("m.txt = %q", got)
	}
}

func TestCreateTwoSpellings(t *testing.T) {
	dir := tree(t, map[string]string{"real/keep.txt": "k\n"})
	if err := os.Symlink("real", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	out, code := splice(t, dir, "link/new/n.txt\n<<<<<<< SEARCH\n=======\nfirst\n>>>>>>> REPLACE\nreal/new/n.txt\n<<<<<<< SEARCH\n=======\nsecond\n>>>>>>> REPLACE\n")
	if code != 1 || !strings.Contains(out, "exists") {
		t.Errorf("exit %d:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "real/new")); err == nil {
		t.Errorf("real/new was made")
	}
}

func TestNoFoldersLeftWhenNothingIsWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to a read-only folder")
	}
	dir := tree(t, map[string]string{"ro/b.txt": "b\n"})
	ro := filepath.Join(dir, "ro")
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o755) })
	out, code := splice(t, dir, "a/b/new.txt\n<<<<<<< SEARCH\n=======\nx\n>>>>>>> REPLACE\nro/b.txt\n<<<<<<< SEARCH\nb\n=======\nB\n>>>>>>> REPLACE\n")
	if code != 1 || !strings.Contains(out, "nothing written") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "a")); err == nil {
		t.Errorf("folder a was left behind")
	}
}

func TestOverlapHint(t *testing.T) {
	dir := tree(t, map[string]string{"a.txt": "aaa\n"})
	out, _ := splice(t, dir, "a.txt\n<<<<<<< SEARCH\naa\n=======\nX\n>>>>>>> REPLACE\n")
	if !strings.Contains(out, "overlap") || strings.Contains(out, `or use "SEARCH all"`) {
		t.Errorf("output:\n%s", out)
	}
}

func TestDiffLinesEdges(t *testing.T) {
	dir := tree(t, map[string]string{"a.txt": "1\n2\n3\n4\n5\n"})
	// block 2 inserts a line right before block 1's line
	out, _ := splice(t, dir, "a.txt\n<<<<<<< SEARCH\n4\n=======\nfour\n>>>>>>> REPLACE\n<<<<<<< SEARCH\n3\n=======\n3\n3b\n>>>>>>> REPLACE\n", "--diff")
	wants(t, out, "block 1 at L5:", "block 2 at L3:")
	// block 2 deletes block 1's line: block 1 is shown where it went
	dir = tree(t, map[string]string{"a.txt": "1\n2\n3\n"})
	out, _ = splice(t, dir, "a.txt\n<<<<<<< SEARCH\n3\n=======\nthree\n>>>>>>> REPLACE\n<<<<<<< SEARCH\n2\nthree\n=======\n>>>>>>> REPLACE\n", "--diff")
	wants(t, out, "block 1 at L2:", "block 2 at L2:")
	if strings.Contains(out, "    + \n") {
		t.Errorf("a deletion shows an empty + line:\n%s", out)
	}
}

func TestSearchAllMany(t *testing.T) {
	src := strings.Repeat("x = old\n", 20000)
	dir := tree(t, map[string]string{"a.txt": src})
	out, code := splice(t, dir, "a.txt\n<<<<<<< SEARCH all\nold\n=======\nnew\n>>>>>>> REPLACE\n", "--diff")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "block 1 at L1, 20000 times:")
	if got := read(t, dir, "a.txt"); got != strings.Repeat("x = new\n", 20000) {
		t.Errorf("a.txt is wrong")
	}
}

func TestNewFileMode(t *testing.T) {
	dir := tree(t, map[string]string{})
	if _, code := splice(t, dir, "n.txt\n<<<<<<< SEARCH\n=======\nx\n>>>>>>> REPLACE\n"); code != 0 {
		t.Fatal("exit", code)
	}
	fi, err := os.Stat(filepath.Join(dir, "n.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if want := newMode(); fi.Mode().Perm() != want {
		t.Errorf("mode %v, want %v", fi.Mode().Perm(), want)
	}
}

func TestFillEmptyFile(t *testing.T) {
	dir := tree(t, map[string]string{"e.txt": ""})
	if out, code := splice(t, dir, "e.txt\n<<<<<<< SEARCH\n=======\nhello\n>>>>>>> REPLACE\n"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := read(t, dir, "e.txt"); got != "hello\n" {
		t.Errorf("e.txt = %q", got)
	}
}

func TestHugeFileNames(t *testing.T) {
	dir := tree(t, map[string]string{"big.go": "package big\n\nfunc f() {}\n" + strings.Repeat("// pad\n", 700000)})
	out, code := splice(t, dir, "big.go:f\n<<<<<<< SEARCH\n{}\n=======\n{ return }\n>>>>>>> REPLACE\n")
	if code != 1 || !strings.Contains(out, "larger than 4 MB") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

// TestScatteredChanges checks the report when one file has more changes
// than one diff takes: each changed function is listed once with its own
// counts, not every function between the first change and the last
// (Sonnet 5.5's re-check).
func TestScatteredChanges(t *testing.T) {
	var src strings.Builder
	src.WriteString("package a\n")
	for i := 0; i < 3000; i++ {
		v := "x"
		if i%2 == 1 {
			v = "y"
		}
		fmt.Fprintf(&src, "\nfunc F%d() {\n\t%s := %d\n\t_ = %s\n}\n", i, v, i, v)
	}
	dir := tree(t, map[string]string{"a.go": src.String()})
	out, code := splice(t, dir, "a.go\n<<<<<<< SEARCH all\n\tx := \n=======\n\tx := -\n>>>>>>> REPLACE\n")
	if code != 0 {
		t.Fatalf("exit %d:\n%.2000s", code, out)
	}
	if n := strings.Count(out, "changed func"); n != 1500 {
		t.Errorf("%d changed functions, want 1500", n)
	}
	if strings.Contains(out, "changed func F1 ") {
		t.Errorf("F1 wasn't changed but is listed")
	}
}

// TestAnchored checks that the anchored diff turns a into b.
func TestAnchored(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for iter := 0; iter < 500; iter++ {
		gen := func() []string {
			out := make([]string, rng.Intn(60))
			for i := range out {
				out[i] = fmt.Sprint(rng.Intn(40))
			}
			return out
		}
		a, b := gen(), gen()
		var got []string
		pos := 0
		for _, h := range anchored(a, b, 0) {
			got = append(got, a[pos:h.os]...)
			got = append(got, b[h.ns:h.ns+h.nc]...)
			pos = h.os + h.oc
		}
		got = append(got, a[pos:]...)
		if strings.Join(got, ",") != strings.Join(b, ",") {
			t.Fatalf("a=%v b=%v gives %v", a, b, got)
		}
	}
}

func TestDiffOfLastLineDeletion(t *testing.T) {
	dir := tree(t, map[string]string{"n.txt": "a\nb"})
	out, _ := splice(t, dir, "n.txt\n<<<<<<< SEARCH\nb\n=======\n>>>>>>> REPLACE\n", "--diff")
	wants(t, out, "block 1 at L2:\n    - b\n")
}

// TestInnermostAll checks the one-pass lookup against ref.Innermost.
func TestInnermostAll(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for iter := 0; iter < 500; iter++ {
		n := 1 + rng.Intn(40)
		var ts []ref.Target
		for k := rng.Intn(8); k > 0; k-- {
			a := 1 + rng.Intn(n)
			ts = append(ts, ref.Target{Path: "f", Line: a, End: a + rng.Intn(n-a+1)})
		}
		got := innermostAll(ts, n)
		for l := 1; l <= n; l++ {
			if want := ref.Innermost(ts, "f", l); got[l-1] != want {
				t.Fatalf("targets %v line %d: %d, want %d", ts, l, got[l-1], want)
			}
		}
	}
}
