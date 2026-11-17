package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const levelSrc = `package level

const speed = 8

// start sets the speed.
func start() {
	v := speed
	println("speed", v, max_speed, speedy)
}

func stop() {
	println(speed)
}
`

const copySh = `#!/bin/sh
cp levels/speed.go levels/fast.go
echo "speed done"
`

func renameTree(t *testing.T) string {
	return tree(t, map[string]string{"levels/speed.go": levelSrc, "scripts/copy.sh": copySh, "notes.txt": "nothing here\n"})
}

// The whole of a rename's report: every changed line under its definition,
// and what stays under the old name.
func TestRename(t *testing.T) {
	dir := renameTree(t)
	out, code := splice(t, dir, "", "--rename", "speed", "pace", ".")
	want := `splice: speed → pace: 7 times on 7 lines in 2 files, written
levels/speed.go
  outside definitions
     3  const pace = 8
  func start L6-9
     5  // start sets the pace.
     7  v := pace
     8  println("pace", v, max_speed, speedy)
  func stop L11-13
    12  println(pace)
scripts/copy.sh
  outside definitions
    2  cp levels/pace.go levels/fast.go
    3  echo "pace done"
note: inside longer words speed stays, twice, the first at levels/speed.go:8; --inside-words renames those too
next: seek speed   what still has the old name, in files not named here
`
	if code != 0 || out != want {
		t.Fatalf("exit %d:\n%s\nwant:\n%s", code, out, want)
	}
	if got := read(t, dir, "levels/speed.go"); got != strings.NewReplacer("const speed", "const pace", "the speed", "the pace", ":= speed", ":= pace", `"speed"`, `"pace"`, "(speed)", "(pace)").Replace(levelSrc) {
		t.Errorf("levels/speed.go is now:\n%s", got)
	}
	if got := read(t, dir, "notes.txt"); got != "nothing here\n" {
		t.Errorf("notes.txt is now %q", got)
	}
}

// --not leaves a line, a definition or a folder alone, and says how much.
func TestRenameNot(t *testing.T) {
	dir := renameTree(t)
	out, code := splice(t, dir, "", "--rename", "speed", "pace", ".", "--not", "scripts/copy.sh:2", "--not", "levels/speed.go:stop")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "speed → pace: 5 times on 5 lines in 2 files, written",
		"note: --not scripts/copy.sh:2 left out once", "note: --not levels/speed.go:stop left out once")
	wants(t, read(t, dir, "scripts/copy.sh"), "cp levels/speed.go levels/fast.go", `echo "pace done"`)
	wants(t, read(t, dir, "levels/speed.go"), "println(speed)", "const pace = 8")

	dir = renameTree(t)
	out, code = splice(t, dir, "", "--rename", "speed", "pace", ".", "--not", "scripts", "--not", "notes.txt")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "5 times on 5 lines in 1 file", "note: --not scripts left out twice",
		"note: --not notes.txt left nothing out: speed isn't there")
	if got := read(t, dir, "scripts/copy.sh"); got != copySh {
		t.Errorf("scripts/copy.sh is now:\n%s", got)
	}
	// A --not that names nothing is a mistake, not nothing to leave out.
	out, code = splice(t, dir, "", "--rename", "pace", "speed", ".", "--not", "levels/speed.go:halt")
	if code != 1 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "nothing written", "--not levels/speed.go:halt: levels/speed.go has no definition halt")
	wants(t, read(t, dir, "levels/speed.go"), "const pace = 8")
}

// A ref renames inside one definition; --inside-words takes longer words
// too; --diff shows the lines as they were.
func TestRenameScopeAndInside(t *testing.T) {
	dir := renameTree(t)
	out, code := splice(t, dir, "", "--rename", "speed", "pace", "levels/speed.go:start", "--inside-words", "--diff")
	want := `splice: speed → pace: 4 times on 2 lines in 1 file, written
levels/speed.go
  func start L6-9
    7  - v := speed
       + v := pace
    8  - println("speed", v, max_speed, speedy)
       + println("pace", v, max_pace, pacey)
next: seek speed   what still has the old name, in files not named here
`
	if code != 0 || out != want {
		t.Fatalf("exit %d:\n%s\nwant:\n%s", code, out, want)
	}
	wants(t, read(t, dir, "levels/speed.go"), "const speed = 8", "println(speed)", "max_pace, pacey")
}

// -n writes nothing; nothing found is a failure that says where the word
// is after all.
func TestRenameDryAndNothing(t *testing.T) {
	dir := renameTree(t)
	out, code := splice(t, dir, "", "-n", "--rename", "speed", "pace", "levels")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "5 times on 5 lines in 1 file, checked, nothing written")
	if got := read(t, dir, "levels/speed.go"); got != levelSrc {
		t.Errorf("-n wrote:\n%s", got)
	}
	out, code = splice(t, dir, "", "--rename", "spee", "pace", "levels", "scripts")
	want := `splice: nothing renamed: spee isn't in levels, scripts as a word of its own
note: inside longer words spee stays, 9 times, the first at levels/speed.go:3; --inside-words renames those too
`
	if code != 1 || out != want {
		t.Fatalf("exit %d:\n%s\nwant:\n%s", code, out, want)
	}
}

// One place that doesn't exist stops everything.
func TestRenameAllOrNothing(t *testing.T) {
	dir := renameTree(t)
	out, code := splice(t, dir, "", "--rename", "speed", "pace", "levels/speed.go", "levels/spede.go", "scripts/copy.sh:9")
	if code != 1 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "speed → pace: 2 of 3 places failed, nothing written", "levels/spede.go: no file levels/spede.go",
		"scripts/copy.sh:9: scripts/copy.sh has 3 lines; the header's line 9 isn't there")
	if got := read(t, dir, "levels/speed.go"); got != levelSrc {
		t.Errorf("it wrote:\n%s", got)
	}
}

// A file named twice, as itself and through its folder and a ref, is
// renamed once, also when the new word holds the old one.
func TestRenameOverlappingPlaces(t *testing.T) {
	dir := renameTree(t)
	out, code := splice(t, dir, "", "--rename", "speed", "top speed", "levels/speed.go", "levels", "levels/speed.go:stop")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "5 times on 5 lines in 1 file")
	wants(t, read(t, dir, "levels/speed.go"), "const top speed = 8", "println(top speed)")
	if strings.Contains(read(t, dir, "levels/speed.go"), "top top") {
		t.Error("renamed twice")
	}
}

// The new word was in the file before: the rename says so.
func TestRenameCollision(t *testing.T) {
	dir := renameTree(t)
	out, code := splice(t, dir, "", "-n", "--rename", "speed", "max_speed", "levels/speed.go")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "note: max_speed was here already, on 1 line (L8): the two are one name now")
}

func TestWordHits(t *testing.T) {
	cases := []struct {
		s, old string
		inside bool
		want   string
	}{
		{"speed speedy max_speed speed2 speed", "speed", false, "[0 30]"},
		{"speed speedy max_speed speed2 speed", "speed", true, "[0 6 17 23 30]"},
		{"a.speed(speed)", "speed", false, "[2 8]"},
		{"größe große größe", "größe", false, "[0 15]"}, // letters beyond ASCII are parts of names
		{"übergröße", "größe", false, "[]"},
		{"x->y x->yz", "->y", false, "[1]"}, // only the end that is part of a name counts
		{"a.b() xa.b()", ".b(", false, "[1 8]"},
		{"aaaa", "aa", true, "[0 2]"}, // left to right, no overlaps
	}
	for _, c := range cases {
		got := fmt.Sprint(wordHits(c.s, c.old, c.inside))
		if got != c.want {
			t.Errorf("%q in %q (inside %v): got %s, want %s", c.old, c.s, c.inside, got, c.want)
		}
	}
}

// More lines than get listed: a count per definition, and --diff lists.
func TestRenameManyLines(t *testing.T) {
	var b strings.Builder
	b.WriteString("package p\n\nfunc f() {\n")
	for i := 0; i < renameList+5; i++ {
		fmt.Fprintf(&b, "\tuse(old, %d)\n", i)
	}
	b.WriteString("}\n")
	dir := tree(t, map[string]string{"p.go": b.String()})
	out, code := splice(t, dir, "", "-n", "--rename", "old", "new", "p.go")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "65 times on 65 lines in 1 file", "  func f L3-69 (65 lines)\n",
		"note: 65 changed lines are more than the 60 that get listed; --diff lists every one")
	if strings.Contains(out, "use(new") {
		t.Errorf("lines listed all the same:\n%s", out)
	}
	out, _ = splice(t, dir, "", "-n", "--diff", "--rename", "old", "new", "p.go")
	wants(t, out, "+ use(new, 64)")
}

// \r\n line endings stay, a long line shows the part around the word, and
// a binary file in a folder is passed over.
func TestRenameFiles(t *testing.T) {
	long := strings.Repeat("x ", 150) + "speed " + strings.Repeat("y ", 150)
	dir := tree(t, map[string]string{"a.txt": "speed\r\nmore speed\r\n", "long.txt": long + "\n", "bin.dat": "speed\x00speed\n"})
	// A link in the folder to a file somewhere else: not the folder's file.
	elsewhere := tree(t, map[string]string{"far.txt": "speed\n"})
	if err := os.Symlink(filepath.Join(elsewhere, "far.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	out, code := splice(t, dir, "", "--rename", "speed", "pace", ".")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := read(t, elsewhere, "far.txt"); got != "speed\n" {
		t.Errorf("the file behind the link is now %q", got)
	}
	wants(t, out, "3 times on 3 lines in 2 files", "…", "x x pace y y")
	if got := read(t, dir, "a.txt"); got != "pace\r\nmore pace\r\n" {
		t.Errorf("a.txt is now %q", got)
	}
	if got := read(t, dir, "bin.dat"); got != "speed\x00speed\n" {
		t.Errorf("bin.dat is now %q", got)
	}
	for _, l := range strings.Split(out, "\n") {
		if len([]rune(l)) > 160 {
			t.Errorf("a line of %d characters: %s", len([]rune(l)), l)
		}
	}
}

// In a repository a folder means the files git sees: not the ignored ones.
func TestRenameGitFolder(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := tree(t, map[string]string{"src/a.go": "package a\n\nvar speed = 1\n", "build/a.go": "package a\n\nvar speed = 1\n", ".gitignore": "build/\n", "new.txt": "speed\n"})
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git init: %v: %s", err, out)
	}
	sub := filepath.Join(dir, "src")
	out, code := splice(t, sub, "", "--rename", "speed", "pace", "..")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "twice on 2 lines in 2 files", "new.txt\n", "src/a.go\n")
	if got := read(t, dir, "build/a.go"); !strings.Contains(got, "speed") {
		t.Errorf("the ignored file was renamed in: %q", got)
	}
	// Named itself, an ignored file is renamed in like any other.
	out, code = splice(t, dir, "", "--rename", "speed", "pace", "build/a.go")
	if code != 0 || !strings.Contains(read(t, dir, "build/a.go"), "pace") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestRenameUsage(t *testing.T) {
	dir := renameTree(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--rename", "speed"}, "--rename takes the old word, the new word, then where"},
		{[]string{"--rename", "speed", "pace"}, "--rename needs to know where"},
		{[]string{"--rename", "speed", "speed", "."}, "the new word is the same as the old"},
		{[]string{"--rename", "", "pace", "."}, "the old word is empty"},
		{[]string{"--rename", "a\nb", "pace", "."}, "takes words, not lines"},
		{[]string{"--rename", "a", "b", ".", "--rename", "c", "d"}, "--rename is there twice"},
		{[]string{"--rename", "a", "b", ".", "--not"}, "--not takes a file, a folder or a ref"},
		{[]string{"--not", "x"}, "--inside-words and --not go with --rename"},
		{[]string{"--inside-words"}, "--inside-words and --not go with --rename"},
	} {
		out, code := splice(t, dir, "", c.args...)
		if code != 2 || !strings.Contains(out, c.want) {
			t.Errorf("%q: exit %d, %s", c.args, code, out)
		}
	}
	// A word may start with a dash.
	if err := os.WriteFile(filepath.Join(dir, "flags.txt"), []byte("run --fast now\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := splice(t, dir, "", "--rename", "--fast", "--quick", "flags.txt")
	if code != 0 || read(t, dir, "flags.txt") != "run --quick now\n" {
		t.Errorf("exit %d:\n%s", code, out)
	}
}
