package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// help colors, the same 256-color palette as glint's, crux's, recap's and seek's help
const (
	hTitle   = "\x1b[38;5;183;1m"
	hDim     = "\x1b[38;5;59m"
	hTagline = "\x1b[38;5;146;3m"
	hSection = "\x1b[38;5;111;1m"
	hShort   = "\x1b[38;5;151m"
	hLong    = "\x1b[38;5;117m"
	hText    = "\x1b[38;5;189m"
	hReset   = "\x1b[0m"
)

type helpFlag struct{ short, long, arg, text string }

type helpPair struct{ name, text string }

// helpBlocks says what the input is; paragraphs are kept as written.
var helpBlocks = []string{
	`Blocks from standard input (a quoted heredoc: splice <<'EOF' ... EOF)
or from the files named. Each block is a header line, then:

  <<<<<<< SEARCH
  the old text, exactly as in the file
  =======
  the new text
  >>>>>>> REPLACE`,
	`The header is a path, or a ref that narrows where the old text is
looked for: main.go:run (inside that definition), main.go:104 (the
definition containing line 104), main.go:80-120 (those lines). A block
without a header line of its own uses the one before it.`,
	`The old text must occur exactly once ("SEARCH all": every time, at
least once). If it isn't there as written, splice also tries it with
spaces at the ends of lines ignored, then with its indentation shifted
(the new text is shifted with it), and says so.`,
	`Empty old text with a ref header (main.go:run) replaces that whole
definition; with a path that doesn't exist, it creates the file. No new
lines at all delete the old text, whole lines with their line breaks.`,
	`Text that contains marker lines goes in a block with longer markers,
all three the same length: <<<<<<<<< SEARCH, =========, >>>>>>>>> REPLACE.`,
}

var helpAfter = []struct {
	title string
	paras []string
}{
	{"ALL OR NOTHING", []string{
		`Every block is checked before anything is written. If one fails,
nothing is written, and each failed block says why: where the nearest
text differs, or where every occurrence is, as refs.`,
		`Blocks run in order, so a later block sees the earlier ones' result.`,
	}},
	{"REPORT", []string{
		`Which definitions changed, as a file header and lines whose path and
name join into a ref (main.go:run). A Go or JSON file that parsed before
and doesn't after gets a "check:" line. A changed signature gets a
"next:" line for its callers.`,
	}},
}

var helpSections = []struct {
	title string
	flags []helpFlag
}{
	{"FLAGS", []helpFlag{
		{"-n", "--dry-run", "", "check and report, write nothing"},
		{"", "--diff", "", "also print each block's old and new lines"},
		{"-A", "", "", "plain output without colors (the default when not on a terminal)"},
		{"-h", "--help", "", "show this help"},
		{"-V", "--version", "", "show the version"},
	}},
}

var helpExamples = []helpPair{
	{"splice <<'EOF' ... EOF", "apply the blocks in the heredoc"},
	{"splice -n edits.txt", "check the blocks in a file, write nothing"},
}

var helpNotes = []helpPair{
	{"exit code", "0 applied, 1 a block failed (nothing written), 2 bad input"},
	{"NO_COLOR", "set it to turn colors off"},
}

// helpText renders the help, colored like the other tools' when color is true.
func helpText(color bool) string {
	paint := func(code, s string) string {
		if !color || s == "" {
			return s
		}
		return code + s + hReset
	}
	const flagWidth = 24
	var b strings.Builder
	section := func(title string) {
		fmt.Fprintf(&b, "\n  %s\n", paint(hSection, title))
	}
	pairs := func(ps []helpPair, w int, nameColor, textColor string) {
		for _, p := range ps {
			w = max(w, len(p.name))
		}
		for _, p := range ps {
			pad := strings.Repeat(" ", w-len(p.name)+2)
			fmt.Fprintf(&b, "    %s%s%s\n", paint(nameColor, p.name), pad, paint(textColor, p.text))
		}
	}

	fmt.Fprintf(&b, "\n  %s %s  %s\n", paint(hTitle, "✦ splice"), paint(hDim, "v"+version),
		paint(hTagline, "checked, batched edits: exact replacements, all or nothing"))

	section("USAGE")
	fmt.Fprintf(&b, "    splice %s %s\n", paint(hDim, "[flags]"), paint(hDim, "[EDITS...]"))

	prose := func(title string, paras []string) {
		section(title)
		for i, p := range paras {
			if i > 0 {
				b.WriteString("\n")
			}
			for _, line := range strings.Split(p, "\n") {
				fmt.Fprintf(&b, "    %s\n", paint(hText, line))
			}
		}
	}
	prose("BLOCKS", helpBlocks)

	for _, s := range helpSections {
		section(s.title)
		for _, f := range s.flags {
			left, plain := "    ", "    "
			if f.short != "" {
				left = paint(hShort, f.short)
				plain = f.short
				if f.long != "" {
					left += paint(hDim, ", ")
					plain += ", "
				}
			}
			if f.long != "" {
				left += paint(hLong, f.long)
				plain += f.long
			}
			if f.arg != "" {
				left += " " + paint(hDim, f.arg)
				plain += " " + f.arg
			}
			fmt.Fprintf(&b, "    %s%s%s\n", left, strings.Repeat(" ", max(flagWidth-len(plain), 2)), paint(hText, f.text))
		}
	}

	for _, a := range helpAfter {
		prose(a.title, a.paras)
	}
	section("EXAMPLES")
	pairs(helpExamples, flagWidth-2, hLong, hDim)
	section("GOOD TO KNOW")
	pairs(helpNotes, 0, hLong, hDim)
	b.WriteString("\n")
	return b.String()
}

// wantsColor reports whether text written to f may be colored.
func wantsColor(f *os.File) bool {
	return term.IsTerminal(int(f.Fd())) && os.Getenv("NO_COLOR") == ""
}
