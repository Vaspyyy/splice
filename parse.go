package main

import (
	"fmt"
	"strings"
)

// The marker lines of a block.
const (
	markSearch  = "<<<<<<< SEARCH"
	markAll     = "<<<<<<< SEARCH all"
	markDivider = "======="
	markReplace = ">>>>>>> REPLACE"
)

// block is one edit: replace Old with New in the place Header names.
type block struct {
	N      int    // 1-based, across all inputs
	Header string // a path or a ref, as written
	Src    string // the input it came from ("" for standard input)
	Line   int    // the line of its <<<<<<< SEARCH marker in Src
	All    bool   // "SEARCH all": every occurrence, not exactly one
	Old    string // the lines between the markers, joined by "\n"
	New    string
	HasOld bool // Old has at least one line (an empty line counts)
	HasNew bool
}

// where names a block for messages: "block 2 (src/pty.rs, line 14)".
func (b *block) where() string {
	at := fmt.Sprintf("line %d", b.Line)
	if b.Src != "" {
		at = fmt.Sprintf("%s:%d", b.Src, b.Line)
	}
	return fmt.Sprintf("block %d (%s, %s)", b.N, b.Header, at)
}

// inputError is a problem with the edit blocks themselves (exit code 2).
type inputError struct{ msg string }

func (e *inputError) Error() string { return e.msg }

// parseBlocks reads the blocks of one input. first is the number the first
// block gets; header is the header carried over from an earlier input.
func parseBlocks(src, text string, first int, header string) ([]*block, string, error) {
	lines := strings.Split(text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1] // the newline at the end of the input ends the last line
	}
	at := func(n int) string {
		if src != "" {
			return fmt.Sprintf("%s:%d", src, n)
		}
		return fmt.Sprintf("line %d", n)
	}
	var (
		blocks  []*block
		pending string // a header line waiting for its block
		pendAt  int
		cur     *block
		state   int // 0 outside, 1 old text, 2 new text
		old     []string
		nw      []string
		divider string // this block's ======= and >>>>>>> REPLACE lines,
		end     string // as long as its <<<<<<< SEARCH marker
	)
	for i, raw := range lines {
		n := i + 1
		line := strings.TrimSuffix(raw, "\r")
		mark := strings.TrimRight(line, " \t")
		switch state {
		case 0:
			width, all, isOpen := openMarker(mark)
			switch {
			case isOpen:
				h := pending
				if h == "" {
					h = header
				}
				if h == "" {
					return nil, "", &inputError{fmt.Sprintf("%s: block without a header: put the file's path (or a ref like main.go:run) on the line before %s", at(n), markSearch)}
				}
				header, pending = h, ""
				cur = &block{N: first + len(blocks), Header: h, Src: src, Line: n, All: all}
				old, nw, state = nil, nil, 1
				divider, end = strings.Repeat("=", width), strings.Repeat(">", width)+" REPLACE"
			case isDivider(mark) || isEnd(mark):
				return nil, "", &inputError{fmt.Sprintf("%s: %s outside a block (missing %s?)", at(n), mark, markSearch)}
			case strings.TrimSpace(line) == "" || isFence(line):
			default:
				if pending != "" {
					return nil, "", &inputError{fmt.Sprintf("%s: text outside a block: %q (only the line right before %s is read, as the header)", at(pendAt), clip(pending, 80), markSearch)}
				}
				pending, pendAt = strings.TrimSpace(line), n
			}
		case 1:
			switch {
			case mark == divider:
				state = 2
			case mark == end:
				return nil, "", &inputError{fmt.Sprintf("%s: %s before %s (block started at line %d)", at(n), end, divider, cur.Line)}
			case sameOpen(mark, len(divider)):
				return nil, "", &inputError{fmt.Sprintf("%s: %s inside a block (block started at line %d; missing %s?)", at(n), mark, cur.Line, end)}
			default:
				old = append(old, line)
			}
		case 2:
			switch {
			case mark == end:
				cur.Old, cur.HasOld = strings.Join(old, "\n"), len(old) > 0
				cur.New, cur.HasNew = strings.Join(nw, "\n"), len(nw) > 0
				blocks = append(blocks, cur)
				cur, state = nil, 0
			case sameOpen(mark, len(divider)):
				return nil, "", &inputError{fmt.Sprintf("%s: %s inside a block (block started at line %d; missing %s?)", at(n), mark, cur.Line, end)}
			default:
				nw = append(nw, line)
			}
		}
	}
	if state != 0 {
		missing := divider
		if state == 2 {
			missing = end
		}
		return nil, "", &inputError{fmt.Sprintf("%s: block not closed: the input ends before %s", at(cur.Line), missing)}
	}
	if pending != "" {
		return nil, "", &inputError{fmt.Sprintf("%s: text outside a block: %q (no block follows it)", at(pendAt), clip(pending, 80))}
	}
	return blocks, header, nil
}

// openMarker reads a block's first line: seven or more '<' and " SEARCH"
// (or " SEARCH all"). Old text that has marker lines of its own goes in a
// block with longer markers, as in Markdown fences.
func openMarker(mark string) (width int, all, ok bool) {
	width = len(mark) - len(strings.TrimLeft(mark, "<"))
	if width < 7 {
		return 0, false, false
	}
	switch mark[width:] {
	case " SEARCH":
		return width, false, true
	case " SEARCH all":
		return width, true, true
	}
	return 0, false, false
}

// sameOpen is an opening marker as long as the current block's.
func sameOpen(mark string, width int) bool {
	w, _, ok := openMarker(mark)
	return ok && w == width
}

func isDivider(mark string) bool {
	return len(mark) >= 7 && strings.Trim(mark, "=") == ""
}

func isEnd(mark string) bool {
	w := len(mark) - len(strings.TrimLeft(mark, ">"))
	return w >= 7 && mark[w:] == " REPLACE"
}

// isFence is a Markdown code fence line: ``` or ```lang.
func isFence(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "```") && !strings.Contains(t[3:], "`") && !strings.ContainsAny(t[3:], " \t")
}

// clip shortens s to about n characters for a message.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
