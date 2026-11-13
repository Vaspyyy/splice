package main

import (
	"bufio"
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vaspyyy/prism/repo"
)

// splice runs the tool in dir with input on standard input and returns its
// output and exit code.
func splice(t *testing.T, dir, input string, args ...string) (string, int) {
	t.Helper()
	t.Chdir(dir)
	var b bytes.Buffer
	w := bufio.NewWriter(&b)
	code := run(args, strings.NewReader(input), false, w, false)
	w.Flush()
	return b.String(), code
}

// tree writes files into a new temporary folder (not a git repository).
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func wants(t *testing.T, out string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(out, p) {
			t.Errorf("output lacks %q:\n%s", p, out)
		}
	}
}

const goSrc = `package main

import "fmt"

// run starts things.
func run(n int) {
	for i := 0; i < n; i++ {
		fmt.Println(i)
	}
}

func helper() {
	fmt.Println(1)
}
`

func TestReplaceAndReport(t *testing.T) {
	dir := tree(t, map[string]string{"main.go": goSrc, "b.txt": "one\ntwo\n"})
	out, code := splice(t, dir, `main.go
<<<<<<< SEARCH
		fmt.Println(i)
=======
		fmt.Println("item", i)
		fmt.Println("done")
>>>>>>> REPLACE

b.txt
<<<<<<< SEARCH
two
=======
three
>>>>>>> REPLACE
`)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "splice: 2 blocks in 2 files, written", "main.go\n  changed func run L6-11 (+2 -1)", "b.txt\n  outside definitions L2")
	if got := read(t, dir, "b.txt"); got != "one\nthree\n" {
		t.Errorf("b.txt = %q", got)
	}
	if !strings.Contains(read(t, dir, "main.go"), "\t\tfmt.Println(\"done\")\n\t}\n") {
		t.Errorf("main.go:\n%s", read(t, dir, "main.go"))
	}
}

func TestAllOrNothing(t *testing.T) {
	dir := tree(t, map[string]string{"main.go": goSrc, "b.txt": "one\ntwo\n"})
	out, code := splice(t, dir, `b.txt
<<<<<<< SEARCH
two
=======
three
>>>>>>> REPLACE
main.go
<<<<<<< SEARCH
fmt.Println(
=======
fmt.Print(
>>>>>>> REPLACE
`)
	if code != 1 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "splice: 1 of 2 blocks failed, nothing written",
		"block 2 (main.go, line 8): the old text occurs 2 times",
		"main.go:8 in run", "main.go:13 in helper", "a header like main.go:run",
		"the other 1 block would apply")
	if got := read(t, dir, "b.txt"); got != "one\ntwo\n" {
		t.Errorf("b.txt was written: %q", got)
	}
}

func TestRefScope(t *testing.T) {
	dir := tree(t, map[string]string{"main.go": goSrc})
	out, code := splice(t, dir, `main.go:helper
<<<<<<< SEARCH
	fmt.Println(
=======
	fmt.Print(
>>>>>>> REPLACE
main.go:8
<<<<<<< SEARCH
fmt.Println(i)
=======
fmt.Print(i)
>>>>>>> REPLACE
`)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	got := read(t, dir, "main.go")
	if !strings.Contains(got, "\tfmt.Print(1)") || !strings.Contains(got, "\t\tfmt.Print(i)") {
		t.Errorf("main.go:\n%s", got)
	}
}

func TestNameErrors(t *testing.T) {
	dir := tree(t, map[string]string{"main.go": goSrc})
	out, code := splice(t, dir, `main.go:hlper
<<<<<<< SEARCH
x
=======
y
>>>>>>> REPLACE
nope/missing.go
<<<<<<< SEARCH
x
=======
y
>>>>>>> REPLACE
main.go:run@HEAD
<<<<<<< SEARCH
x
=======
y
>>>>>>> REPLACE
`)
	if code != 1 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "main.go has no definition hlper", "main.go:helper", "no file nope/missing.go")
}

func TestNearest(t *testing.T) {
	dir := tree(t, map[string]string{"main.go": goSrc})
	out, code := splice(t, dir, `main.go
<<<<<<< SEARCH
	for i := 0; i < n; i++ {
		fmt.Println(item, i)
	}
=======
x
>>>>>>> REPLACE
`)
	if code != 1 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "the old text isn't in main.go", "nearest: main.go:7-9 in run",
		"the 2nd line of the old text differs:", "file:  →→fmt.Println(i)", "block: →→fmt.Println(item, i)")
}

func TestAppliedAlready(t *testing.T) {
	dir := tree(t, map[string]string{"main.go": goSrc})
	out, _ := splice(t, dir, `main.go
<<<<<<< SEARCH
		fmt.Println(x)
=======
		fmt.Println(i)
	}
>>>>>>> REPLACE
`)
	wants(t, out, "but the new text is, at main.go:8 in run: was this block applied already?")
}

func TestTolerant(t *testing.T) {
	src := "def f():\n    if x:   \n        a = 1\n    return a\n"
	dir := tree(t, map[string]string{"a.py": src})
	out, code := splice(t, dir, `a.py
<<<<<<< SEARCH
    if x:
        a = 1
=======
    if x:
        a = 2
>>>>>>> REPLACE
`)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "matched ignoring spaces at the ends of lines")
	if got := read(t, dir, "a.py"); got != "def f():\n    if x:\n        a = 2\n    return a\n" {
		t.Errorf("a.py = %q", got)
	}

	// written without its indentation: the new text is moved in with it
	dir = tree(t, map[string]string{"a.py": src})
	out, code = splice(t, dir, `a.py
<<<<<<< SEARCH
if x:
    a = 1
return a
=======
if x:
    a = 1
    b = 2
return a + b
>>>>>>> REPLACE
`)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "matched 4 spaces further in")
	if got := read(t, dir, "a.py"); got != "def f():\n    if x:\n        a = 1\n        b = 2\n    return a + b\n" {
		t.Errorf("a.py = %q", got)
	}

	// more indentation than the file: moved out, unless the new text can't be
	dir = tree(t, map[string]string{"a.py": src})
	out, code = splice(t, dir, "a.py\n<<<<<<< SEARCH\n          a = 1\n      return a\n=======\n          a = 1\n return a\n>>>>>>> REPLACE\n")
	if code != 1 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "less indentation than that")
}

func TestLineEndings(t *testing.T) {
	dir := tree(t, map[string]string{"w.txt": "a\r\nb\r\nc\r\n", "n.txt": "a\nb"})
	out, code := splice(t, dir, "w.txt\r\n<<<<<<< SEARCH\r\nb\r\n=======\r\nB\r\nB2\r\n>>>>>>> REPLACE\r\nn.txt\n<<<<<<< SEARCH\nb\n=======\nB\n>>>>>>> REPLACE\n")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := read(t, dir, "w.txt"); got != "a\r\nB\r\nB2\r\nc\r\n" {
		t.Errorf("w.txt = %q", got)
	}
	if got := read(t, dir, "n.txt"); got != "a\nB" {
		t.Errorf("n.txt = %q", got)
	}
}

func TestWholeDefinition(t *testing.T) {
	src := "class A:\n    def f(self):\n        return 1\n\n    def g(self):\n        return 2\n"
	dir := tree(t, map[string]string{"a.py": src})
	out, code := splice(t, dir, `a.py:A.f
<<<<<<< SEARCH
=======
def f(self, x):
    return x
>>>>>>> REPLACE
`)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "changed def A.f", "moved 4 spaces further in", "next: seek a.py:A.f")
	want := "class A:\n    def f(self, x):\n        return x\n\n    def g(self):\n        return 2\n"
	if got := read(t, dir, "a.py"); got != want {
		t.Errorf("a.py = %q", got)
	}

	out, code = splice(t, dir, "a.py:A.g\n<<<<<<< SEARCH\n=======\n>>>>>>> REPLACE\n")
	if code != 1 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "deleting a definition this way isn't supported yet")
}

func TestCreate(t *testing.T) {
	dir := tree(t, map[string]string{"a.txt": "x\n"})
	out, code := splice(t, dir, "sub/new.md\n<<<<<<< SEARCH\n=======\n# Title\n\n## Part\n>>>>>>> REPLACE\n")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "sub/new.md\n  new file, 3 lines, 2 definitions")
	if got := read(t, dir, "sub/new.md"); got != "# Title\n\n## Part\n" {
		t.Errorf("new.md = %q", got)
	}
	out, code = splice(t, dir, "a.txt\n<<<<<<< SEARCH\n=======\ny\n>>>>>>> REPLACE\n")
	if code != 1 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "which creates a file, but a.txt exists")
}

func TestSearchAll(t *testing.T) {
	dir := tree(t, map[string]string{"a.txt": "x = 1\ny = x\nz = x\n"})
	out, code := splice(t, dir, "a.txt:2-3\n<<<<<<< SEARCH all\nx\n=======\nw\n>>>>>>> REPLACE\n", "--diff")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "block 1 at L2, 2 times:")
	if got := read(t, dir, "a.txt"); got != "x = 1\ny = w\nz = w\n" {
		t.Errorf("a.txt = %q", got)
	}
}

// TestReportOwners checks that doc comments and blank lines of a new
// definition don't show as changes outside definitions.
func TestReportOwners(t *testing.T) {
	dir := tree(t, map[string]string{"a.go": "package a\n\ntype T struct{}\n"})
	out, code := splice(t, dir, `a.go
<<<<<<< SEARCH
type T struct{}
=======
type T struct{}

// M does things.
//
// More about it.
func (T) M() {}

// x is one.
var x = 1
>>>>>>> REPLACE
`)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "added func T.M L8 (+4 -0)", "outside definitions L10-11")
}

// TestDelete checks that a block without new lines deletes whole lines with
// their line breaks, and that one empty new line leaves an empty line.
func TestDelete(t *testing.T) {
	src := "package a\n\n// List lists.\nfunc List() {}\n\n// Keep stays.\nfunc Keep() {}\n"
	dir := tree(t, map[string]string{"a.go": src, "b.txt": "x\ndebug\ny\ndebug\n", "c.txt": "keep a b\n"})
	out, code := splice(t, dir, `a.go
<<<<<<< SEARCH
// List lists.
func List() {}

=======
>>>>>>> REPLACE
b.txt
<<<<<<< SEARCH all
debug
=======
>>>>>>> REPLACE
c.txt
<<<<<<< SEARCH
 a
=======
>>>>>>> REPLACE
`)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "removed func List (+0 -2)")
	if got := read(t, dir, "a.go"); got != "package a\n\n// Keep stays.\nfunc Keep() {}\n" {
		t.Errorf("a.go = %q", got)
	}
	if got := read(t, dir, "b.txt"); got != "x\ny\n" {
		t.Errorf("b.txt = %q", got)
	}
	if got := read(t, dir, "c.txt"); got != "keep b\n" {
		t.Errorf("c.txt = %q", got)
	}
	dir = tree(t, map[string]string{"d.txt": "a\nb\nc\n"})
	splice(t, dir, "d.txt\n<<<<<<< SEARCH\nb\n=======\n\n>>>>>>> REPLACE\n")
	if got := read(t, dir, "d.txt"); got != "a\n\nc\n" {
		t.Errorf("d.txt = %q", got)
	}
}

func TestDryRun(t *testing.T) {
	dir := tree(t, map[string]string{"a.txt": "a\n"})
	out, code := splice(t, dir, "a.txt\n<<<<<<< SEARCH\na\n=======\nb\n>>>>>>> REPLACE\n", "-n")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "checked, nothing written")
	if got := read(t, dir, "a.txt"); got != "a\n" {
		t.Errorf("a.txt was written: %q", got)
	}
}

func TestSymlinkAndMode(t *testing.T) {
	dir := tree(t, map[string]string{"real.sh": "echo a\n"})
	if err := os.Chmod(filepath.Join(dir, "real.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.sh", filepath.Join(dir, "link.sh")); err != nil {
		t.Fatal(err)
	}
	out, code := splice(t, dir, "link.sh\n<<<<<<< SEARCH\necho a\n=======\necho b\n>>>>>>> REPLACE\n")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	fi, err := os.Lstat(filepath.Join(dir, "link.sh"))
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link.sh is no longer a symlink")
	}
	fi, _ = os.Stat(filepath.Join(dir, "real.sh"))
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
	if got := read(t, dir, "real.sh"); got != "echo b\n" {
		t.Errorf("real.sh = %q", got)
	}
}

func TestChecks(t *testing.T) {
	dir := tree(t, map[string]string{"a.json": "{\"a\": 1,\n \"b\": 2}\n", "bad.json": "{\n"})
	out, _ := splice(t, dir, `a.json
<<<<<<< SEARCH
 "b": 2}
=======
 "b": 2,}
>>>>>>> REPLACE
bad.json
<<<<<<< SEARCH
{
=======
{
>>>>>>> REPLACE
`)
	wants(t, out, "check: a.json:2:")
	if strings.Contains(out, "check: bad.json") {
		t.Errorf("a file that didn't parse before got a check line:\n%s", out)
	}
}

func TestChangedOnDisk(t *testing.T) {
	dir := tree(t, map[string]string{"a.txt": "a\n"})
	t.Chdir(dir)
	blocks, _, err := parseBlocks("", "a.txt\n<<<<<<< SEARCH\na\n=======\nb\n>>>>>>> REPLACE\n", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	s := newSession(mustDetect(t, dir), dir)
	s.apply(blocks[0])
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("other\n"), 0o644)
	if _, err := s.write(); err == nil || !strings.Contains(err.Error(), "changed on disk") {
		t.Errorf("err = %v", err)
	}
	if got := read(t, dir, "a.txt"); got != "other\n" {
		t.Errorf("a.txt = %q", got)
	}
}

func TestParseErrors(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"<<<<<<< SEARCH\na\n=======\nb\n>>>>>>> REPLACE\n", "block without a header"},
		{"a.txt\n<<<<<<< SEARCH\na\n=======\nb\n", "block not closed"},
		{"a.txt\n<<<<<<< SEARCH\na\n>>>>>>> REPLACE\n", "before ======="},
		{"some prose\na.txt\n<<<<<<< SEARCH\na\n=======\nb\n>>>>>>> REPLACE\n", `line 1: text outside a block: "some prose"`},
		{"a.txt\n", "no block follows it"},
		{"", "no blocks in the input"},
		{"=======\n", "outside a block"},
	} {
		out, code := splice(t, t.TempDir(), c.in)
		if code != 2 || !strings.Contains(out, c.want) {
			t.Errorf("input %q: exit %d, want %q in:\n%s", c.in, code, c.want, out)
		}
	}
}

func TestParseBlocks(t *testing.T) {
	in := "```\na.txt\n<<<<<<< SEARCH\nx\n\n=======\n>>>>>>> REPLACE\n```\n\n<<<<<<< SEARCH all  \ny\n=======\nz\n>>>>>>> REPLACE\n"
	bs, _, err := parseBlocks("", in, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 2 {
		t.Fatalf("%d blocks", len(bs))
	}
	if b := bs[0]; b.Header != "a.txt" || b.Old != "x\n" || !b.HasOld || b.HasNew || b.New != "" {
		t.Errorf("block 1 = %+v", *b)
	}
	if b := bs[1]; b.Header != "a.txt" || !b.All || b.Old != "y" || b.New != "z" || b.N != 2 {
		t.Errorf("block 2 = %+v", *b)
	}

	// longer markers: only markers of the same length end the block
	in = "a.txt\n<<<<<<<<< SEARCH\n<<<<<<< SEARCH\n=======\n=========\n>>>>>>> REPLACE\n>>>>>>>>> REPLACE\n"
	bs, _, err = parseBlocks("", in, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 1 || bs[0].Old != "<<<<<<< SEARCH\n=======" || bs[0].New != ">>>>>>> REPLACE" {
		t.Errorf("blocks = %+v", bs)
	}
}

// TestDiffLines checks that the hunks turn a into b and are as few lines as
// a longest common subsequence allows.
func TestDiffLines(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	gen := func() []string {
		n := rng.Intn(30)
		out := make([]string, n)
		for i := range out {
			out[i] = string(rune('a' + rng.Intn(4)))
		}
		return out
	}
	for iter := 0; iter < 2000; iter++ {
		a, b := gen(), gen()
		hs := diffLines(a, b)
		var got []string
		pos, changed := 0, 0
		for _, h := range hs {
			got = append(got, a[pos:h.os]...)
			got = append(got, b[h.ns:h.ns+h.nc]...)
			pos = h.os + h.oc
			changed += h.oc + h.nc
		}
		got = append(got, a[pos:]...)
		if strings.Join(got, ",") != strings.Join(b, ",") {
			t.Fatalf("a=%v b=%v hunks=%v gives %v", a, b, hs, got)
		}
		if want := len(a) + len(b) - 2*lcs(a, b); changed != want {
			t.Fatalf("a=%v b=%v: %d changed lines, want %d", a, b, changed, want)
		}
	}
}

func mustDetect(t *testing.T, dir string) *repo.Info {
	t.Helper()
	info, err := repo.Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func lcs(a, b []string) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	return dp[0][0]
}
