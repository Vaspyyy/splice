# splice: design

splice applies a batch of exact text replacements to files, checks every one
of them before it writes anything, and reports what changed by symbol.

## Why

In the agent transcripts of 2026-10-14 to 2026-11-13, agents edited files with
inline Python 6,294 times and with the Edit tool 2,336 times. Nearly all of
those Python edits have the same shape:

```python
p = "src/pty.rs"
s = open(p).read()
old = "                        lease = None;\n"
assert s.count(old) == 1
open(p, "w").write(s.replace(old, "                        drop(lease.take());\n"))
```

Exact old/new pairs (about half have 2 or more per call), a check that each old
text occurs exactly once (in about 70%), sometimes several files, then a build
in the same command. Agents reach for Python because the Edit tool takes one
replacement per call. Python works, but:

- a forgotten `assert` turns a mismatch into a silent no-op, and
  `s.replace` without a count replaces every occurrence;
- a failed assert halfway through a batch leaves the earlier files written;
- a mismatch says only `AssertionError`, not where the text differs;
- the heredoc, quoting and `open().read()` boilerplate cost tokens every time.

splice keeps what makes the Python approach attractive (many edits, many files,
one call, no prior read needed) and fixes those four problems.

## Input

Edits are blocks in the SEARCH/REPLACE format many models already know:

```
src/pty.rs
<<<<<<< SEARCH
                        lease = None;
=======
                        drop(lease.take());
>>>>>>> REPLACE
```

splice reads them from standard input, or from files named as arguments
(`splice edits.txt`; `-` is standard input). An agent writes them in a quoted
heredoc, so nothing needs escaping:

```sh
splice <<'EOF'
src/pty.rs
<<<<<<< SEARCH
...
>>>>>>> REPLACE
EOF
```

### Grammar

- A block is a line `<<<<<<< SEARCH`, the old text, a line `=======`, the new
  text, and a line `>>>>>>> REPLACE`. Marker lines must match exactly (trailing
  spaces and a `\r` are ignored). The old and new text are taken verbatim; the
  newline before `=======` and before `>>>>>>> REPLACE` belongs to the marker,
  not the text.
- Markers may be longer than seven characters, as long as all three of a block
  have the same length (`<<<<<<<<< SEARCH`, `=========`, `>>>>>>>>> REPLACE`).
  Inside such a block, shorter marker lines are text, so a block can edit a
  file that contains blocks (tests, docs about the format), the way a longer
  Markdown fence holds a shorter one.
- The **header** is the last non-blank line before a block's `<<<<<<< SEARCH`:
  a path or a [prism ref](https://github.com/Vaspyyy/prism/blob/main/docs/refs.md).
  A block with no header line of its own uses the previous block's header.
- Blank lines and Markdown fence lines (```` ``` ````, ```` ```diff ````)
  between blocks are ignored. Any other text outside blocks is an error: it
  usually means a header or marker went missing, and guessing would edit the
  wrong place.
- A second `=======` in a block is an error, not text: it is what forgetting
  the longer markers looks like. Text with such lines needs longer markers.
- `<<<<<<< SEARCH all` replaces every occurrence instead of exactly one.
- No lines at all for the new text deletes the old text; when the old text is
  whole lines, their line breaks go with them. One empty line as the new text
  leaves an empty line.

### What a block does

| Header | SEARCH text | Effect |
| --- | --- | --- |
| `path` | text | replace its one occurrence in the file |
| `path:Name` | text | replace its one occurrence inside that definition |
| `path:104` | text | replace it inside the innermost definition containing line 104 |
| `path:81-200` | text | replace it inside lines 81 to 200 |
| `path:Name` or `path:104` | empty | replace the whole definition (its defining line to its last line, without blank lines at its end) with the new text |
| `path` (doesn't exist) | empty | create the file with the new text |

Any header plus `SEARCH all`: replace every exact occurrence in that scope, at
least one. (The tolerant matches below are for single occurrences only.)

A ref's path is resolved like every prism tool does (repo-relative,
cwd-relative, absolute, or a unique suffix such as `pty.rs`), and its name is
matched with `ref.Match` (`run`, `Server.run`, `parse#2`). A ref that names
several definitions is refused with all of them listed as refs; one that names
none lists the file's definitions it might mean. `@rev` makes no sense for an
edit and is refused.

Replacing a whole definition keeps the doc comment above it: the range is the
one prism refs use, defining line to last line. To change the doc comment too,
use a SEARCH block. (Deleting a definition and replacing its doc with it need
the doc comment's range, which only seek knows today; see "Later".)

## Matching

1. **Line endings.** In a file with CRLF line endings, the block's text is
   converted to CRLF before matching and writing. A file's final newline (or
   its absence) is kept.
2. **Exact.** The old text is searched as is, as a substring (it doesn't have
   to be whole lines). Overlapping occurrences count, so a count of one
   really means unique; `SEARCH all` refuses occurrences that overlap. Old
   text that starts with spaces or tabs only matches where a run of them
   starts: its indentation says which line it means, so `    foo` doesn't
   match the end of `        foo`. Old text that is only blank lines is
   refused.
3. **If there is no exact match**, two tolerant matches are tried, line by
   line, in this order. Each must be unique in the scope:
   - **trailing whitespace** ignored at the end of every line;
   - **indentation shifted**: every non-blank line of the old text matches the
     file with the same prefix of leading whitespace added or removed. The new
     text is re-indented by the same amount, so a block written without its
     indentation lands correctly.
   A tolerant match is reported on its line ("matched ignoring spaces at the
   ends of lines", "matched 8 spaces further in"), never silent. Every place
   that matches counts, even one whose new text couldn't be shifted, so an
   ambiguous block is refused rather than applied to the other place. In a
   file with mixed line endings, lines that end in `\r` give the new text
   their `\r`.
4. **Nothing matched:** the block fails and splice shows the **nearest** place:
   the lines of the scope most like the old text, with the first line that
   differs from both sides:

   ```
   block 2 (src/pty.rs:Pty.close, line 14): the old text isn't in src/pty.rs:Pty.close
     nearest: src/pty.rs:211-213 in Pty.close
     the 2nd line of the old text differs:
       file:  if let Some(l) = lease.take() {
       block: if let Some(l) = lease {
   ```

   Tabs show as `→` and spaces at the end of a line as `·`, so a difference in
   whitespace is visible. When the old text isn't there but the new text is,
   splice says so instead ("was this block applied already?").

5. **More than one match** (without `all`): the block fails and every
   occurrence is listed as a ref with the definition containing it, plus how
   to make it unique (more lines, a ref header, or `SEARCH all`):

   ```
   block 1 (src/app.ts, line 2): the old text occurs 3 times
     src/app.ts:41 in App.render
     src/app.ts:88 in App.update
     src/app.ts:130 in helper
     make it unique with more lines or a header like src/app.ts:App.render, or use "SEARCH all" to replace every one
   ```

## Order and atomicity

- Blocks run in order. Several blocks on one file edit it one after the
  other in memory, so a later block sees the earlier ones' result, and its ref
  is resolved against that result.
- **All or nothing.** splice reads, resolves and applies every block in
  memory first. If any block fails, it writes nothing, reports every failed
  block (not only the first) and exits 1.
- Then every changed file is written to a temporary file in its directory,
  with its mode (setuid, setgid and sticky bits too). Only when all of them
  are written are they renamed over the old files. A file that changed on
  disk since splice read it, or a folder it can't write to, stops the batch
  with nothing written. Only a failed rename (a folder that changed under
  splice) can leave some files written; splice then says which.
- One file reached two ways (a symlinked folder, a hard link) is one buffer:
  splice resolves every path to the real file before it reads it. A file
  with more than one hard link is written in place after staging, not
  renamed, since a rename would leave its other names on the old content.
- A block whose new text equals its old text is fine, but noted.

## Output

Grouped like recap: a file header, then one line per changed definition, so
header path + `:` + name is a ref.

```
splice: 3 blocks in 2 files, written
art/export.py
  changed def export_outline L120-131 (+4 -0)
src/pty.rs
  changed fn Pty.close L88-102 (+1 -1)
  added fn Pty.drain L104-110 (+9 -0)
  note: block 3: matched ignoring spaces at the ends of lines
next: seek src/pty.rs:Pty.close   its callers: its signature changed
```

- A definition's doc comment and decorators (the non-blank lines right above
  it) count as part of it.
- Other changed lines outside any definition show as `outside definitions
  L3-5`; blank lines don't.
- A definition whose signature line changed gets a `next:` line for
  `seek path:Name`, which lists its callers (at most three such lines).
- A new file shows as `new file, 40 lines`.
- `-n` (`--dry-run`) checks and reports the same way, ending in
  `checked, nothing written`.
- `--diff` adds the changed lines of each block under its line (`-` old,
  `+` new), for when the agent wants to see the result once.
- Failures, as above, then `splice: nothing written: 1 of 3 blocks failed`.

### Checks after editing

For Go files (`go/parser`) and JSON files (`encoding/json`), splice compares
the file before and after: when it parsed before and doesn't after, it adds a
warning with the location (`check: src/main.go:42:3: expected '}', found
'EOF'`). It still writes: an agent may fix it with its next block or call.
Other languages get no check; the build the agent runs next is the check.

### Exit codes

0 every block applied (or checked with `-n`), 1 a block failed and nothing
was written, 2 bad input (unreadable edits, a broken block, bad flags).

### JSON

`--json` prints prism's envelope with a `report.Splice` result. The type
belongs in `prism/report` like every tool's result, so it waits for a prism
release; v0.1 has text output only.

## Not doing

- **Regular-expression replacement.** 105 of the 6,294 Python edits used
  `re.sub`. A regex edit can't be checked the way an exact one can.
- **Renaming a symbol across a repository.** That needs a real parser per
  language (an LSP); prism's outline is deliberately not one.
- **Fuzzy matching beyond whitespace.** A block whose words differ from the
  file is wrong; the nearest-match report helps fix it, splice doesn't guess.
- **Whole-file writes.** The Write tool does that; splice creates a file only
  when it doesn't exist.

## Later

- `--json` once `report.Splice` is in prism.
- Insert a new definition before or after a named one (`<<<<<<< AFTER` with a
  ref header), the second most common shape after replacement.
- Delete a definition (empty SEARCH and REPLACE with a ref header), and let a
  whole-definition replacement that starts with a comment replace the doc
  comment. Both need the doc comment's range, which is seek's private
  `docStart` today; it would move to `prism/outline`.
