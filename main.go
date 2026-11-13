// splice applies a batch of exact text replacements to files. It checks every
// one before it writes anything, refuses a block whose old text is missing
// or occurs more than once (saying where the nearest match differs, or
// listing every occurrence as a ref), and reports what changed by symbol.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Vaspyyy/prism/repo"
	"golang.org/x/term"
)

const version = "0.1.2"

type options struct {
	dry, diff, plain bool
	inputs           []string
}

func main() {
	out := bufio.NewWriterSize(os.Stdout, 64<<10)
	stdinTTY := term.IsTerminal(int(os.Stdin.Fd()))
	tty := term.IsTerminal(int(os.Stdout.Fd()))
	code := run(os.Args[1:], os.Stdin, stdinTTY, out, tty)
	out.Flush()
	os.Exit(code)
}

func run(args []string, stdin io.Reader, stdinTTY bool, out *bufio.Writer, tty bool) int {
	o, code, msg := parse(args)
	if msg != "" {
		if code == 0 {
			fmt.Fprint(out, msg)
			return 0
		}
		fmt.Fprintln(out, "splice: "+msg)
		return code
	}
	color := tty && !o.plain && os.Getenv("NO_COLOR") == ""
	p := painter(color)

	if len(o.inputs) == 0 {
		if stdinTTY {
			fmt.Fprintln(out, "splice: reads edit blocks from standard input or from files named as arguments; splice -h shows how")
			return 2
		}
		o.inputs = []string{"-"}
	}
	var blocks []*block
	header := ""
	for _, in := range o.inputs {
		var data []byte
		var err error
		src := in
		if in == "-" {
			data, err = io.ReadAll(stdin)
			src = ""
		} else {
			data, err = os.ReadFile(in)
		}
		if err != nil {
			fmt.Fprintf(out, "splice: %v\n", err)
			return 2
		}
		bs, h, err := parseBlocks(src, string(data), len(blocks)+1, header)
		if err != nil {
			fmt.Fprintf(out, "splice: %v\n", err)
			return 2
		}
		blocks, header = append(blocks, bs...), h
	}
	if len(blocks) == 0 {
		fmt.Fprintf(out, "splice: no blocks in the input; a block is a header line (a path), then %s, the old text, %s, the new text and %s\n", markSearch, markDivider, markReplace)
		return 2
	}

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
	s := newSession(info, cwd)
	for _, b := range blocks {
		s.apply(b)
	}

	if len(s.failed) > 0 {
		fmt.Fprintf(out, "%s\n", p.fail(fmt.Sprintf("splice: %d of %s failed, nothing written", len(s.failed), plural(len(blocks), "block"))))
		for _, fl := range s.failed {
			fmt.Fprintf(out, "%s: %s\n", p.head(fl.b.where()), fl.msg)
			for _, d := range fl.detail {
				fmt.Fprintf(out, "  %s\n", d)
			}
		}
		if ok := len(blocks) - len(s.failed); ok > 0 {
			fmt.Fprintf(out, "%s\n", p.dim(fmt.Sprintf("the other %s would apply", plural(ok, "block"))))
		}
		return 1
	}

	var changed []*file
	for _, f := range s.order {
		if f.changed() {
			changed = append(changed, f)
		}
	}
	reports := make([]*fileReport, len(changed))
	for i, f := range changed {
		reports[i] = summarize(f)
	}

	var written []*file
	var werr error
	if !o.dry {
		written, werr = s.write()
	}

	files := plural(len(changed), "file")
	switch {
	case len(changed) == 0:
		fmt.Fprintf(out, "splice: %s, nothing changed\n", plural(len(blocks), "block"))
	case o.dry:
		fmt.Fprintf(out, "splice: %s in %s, checked, nothing written\n", plural(len(blocks), "block"), files)
	case werr != nil && len(written) == 0:
		fmt.Fprintf(out, "%s\n", p.fail(fmt.Sprintf("splice: nothing written: %v", werr)))
		return 1
	case werr != nil:
		fmt.Fprintf(out, "%s\n", p.fail(fmt.Sprintf("splice: %s in %s; wrote %d of %s, then: %v", plural(len(blocks), "block"), files, len(written), files, werr)))
	default:
		fmt.Fprintf(out, "splice: %s in %s, written\n", plural(len(blocks), "block"), files)
	}
	var next []string
	for _, fr := range reports {
		render(out, p, fr, o.diff)
		for _, c := range fr.changes {
			if c.sigChanged && len(next) < 3 {
				next = append(next, fmt.Sprintf("next: seek %s:%s   its callers: its signature changed", fr.f.rel, c.name))
			}
		}
	}
	for _, f := range s.order {
		if !f.changed() {
			for _, n := range f.notes {
				fmt.Fprintf(out, "%s\n", p.dim("note: "+n))
			}
		}
	}
	for _, n := range next {
		fmt.Fprintln(out, p.dim(n))
	}
	if werr != nil {
		var left []string
		done := map[*file]bool{}
		for _, f := range written {
			done[f] = true
		}
		for _, f := range changed {
			if !done[f] {
				left = append(left, f.rel)
			}
		}
		fmt.Fprintf(out, "not written: %s\n", strings.Join(left, ", "))
		return 1
	}
	return 0
}

// render prints one file's changes.
func render(out *bufio.Writer, p painter, fr *fileReport, diff bool) {
	f := fr.f
	fmt.Fprintln(out, p.head(f.rel))
	if f.created {
		what := fmt.Sprintf("new file, %s", plural(fr.newLines, "line"))
		if fr.defs > 0 {
			what += ", " + plural(fr.defs, "definition")
		}
		fmt.Fprintf(out, "  %s\n", p.verb("added", what))
	}
	for _, c := range fr.changes {
		at := ""
		if c.line > 0 {
			at = " " + lineRange(c.line, c.end)
		}
		fmt.Fprintf(out, "  %s %s %s%s %s\n", p.verb(c.verb, c.verb), c.kind, c.name, at, p.dim(fmt.Sprintf("(+%d -%d)", c.plus, c.minus)))
	}
	if len(fr.outside) > 0 {
		var rs []string
		for _, r := range fr.outside {
			rs = append(rs, lineRange(r[0], r[1]))
		}
		fmt.Fprintf(out, "  outside definitions %s\n", strings.Join(rs, ", "))
	}
	for _, n := range f.notes {
		fmt.Fprintf(out, "  %s\n", p.dim("note: "+n))
	}
	if fr.check != "" {
		fmt.Fprintf(out, "  %s\n", p.fail("check: "+fr.check+" (written anyway)"))
	}
	if diff {
		for _, e := range f.edits {
			line := strings.Count(f.buf[:min(e.off, len(f.buf))], "\n") + 1
			if e.pulled { // a deleted last line took the line break before it
				line++
			}
			head := fmt.Sprintf("block %d at L%d", e.b.N, line)
			if e.count > 1 {
				head += fmt.Sprintf(", %d times", e.count)
			}
			fmt.Fprintf(out, "  %s\n", p.dim(head+":"))
			old := strings.TrimSuffix(e.old, "\n")
			if e.pulled {
				old = strings.TrimPrefix(strings.TrimPrefix(old, "\r"), "\n")
			}
			if e.old != "" {
				for _, l := range strings.Split(old, "\n") {
					fmt.Fprintf(out, "    %s\n", p.verb("removed", "- "+strings.TrimSuffix(l, "\r")))
				}
			}
			if e.b.HasNew {
				for _, l := range strings.Split(e.new, "\n") {
					fmt.Fprintf(out, "    %s\n", p.verb("added", "+ "+strings.TrimSuffix(l, "\r")))
				}
			}
		}
	}
}

// painter colors output on a terminal.
type painter bool

func (p painter) wrap(code, s string) string {
	if !p || s == "" {
		return s
	}
	return code + s + "\x1b[0m"
}
func (p painter) head(s string) string { return p.wrap("\x1b[38;5;189;1m", s) }
func (p painter) dim(s string) string  { return p.wrap("\x1b[38;5;102m", s) }
func (p painter) fail(s string) string { return p.wrap("\x1b[38;5;210m", s) }
func (p painter) verb(verb, s string) string {
	switch verb {
	case "added":
		return p.wrap("\x1b[38;5;151m", s)
	case "removed":
		return p.wrap("\x1b[38;5;210m", s)
	}
	return p.wrap("\x1b[38;5;222m", s)
}

// parse reads the command line. A message with code 0 is printed as is
// (help, version); with another code it's an error.
func parse(args []string) (options, int, string) {
	var o options
	flags := true
	for _, a := range args {
		switch {
		case !flags || a == "-" || !strings.HasPrefix(a, "-"):
			o.inputs = append(o.inputs, a)
		case a == "--":
			flags = false
		case a == "-h" || a == "--help":
			return o, 0, helpText(wantsColor(os.Stdout))
		case a == "-V" || a == "--version":
			return o, 0, "splice " + version + "\n"
		case a == "-n" || a == "--dry-run":
			o.dry = true
		case a == "--diff":
			o.diff = true
		case a == "-A":
			o.plain = true
		default:
			return o, 2, fmt.Sprintf("unknown flag %s (splice -h lists them)", a)
		}
	}
	return o, 0, ""
}
