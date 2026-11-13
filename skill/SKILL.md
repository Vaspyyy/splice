---
name: splice
description: Edit files with `splice` instead of inline Python (`s.replace(old, new)` with asserts), `sed -i`, or a row of Edit calls whenever you make more than one replacement, in one file or several. It takes SEARCH/REPLACE blocks in a quoted heredoc, checks all of them before writing anything (all or nothing), refuses old text that is missing or not unique and says where the nearest text differs or lists every occurrence, and reports which definitions changed. A header like `main.go:run` limits a block to one definition, and an empty SEARCH with such a header replaces the whole definition. Use it for any batch of edits.
---

# splice for agents

`splice` applies SEARCH/REPLACE blocks. Give them in a quoted heredoc, so nothing needs escaping:

```sh
splice <<'EOF'
src/pty.rs
<<<<<<< SEARCH
                        lease = None;
=======
                        drop(lease.take());
>>>>>>> REPLACE

src/server.go:Server.run
<<<<<<< SEARCH
	log.Printf("start")
=======
	log.Printf("start %s", s.addr)
>>>>>>> REPLACE
EOF
```

```
splice: 2 blocks in 2 files, written
src/pty.rs
  changed fn Pty.close L88-102 (+1 -1)
src/server.go
  changed func Server.run L40-71 (+1 -1)
```

A header line, then `<<<<<<< SEARCH`, the old text, `=======`, the new text, `>>>>>>> REPLACE`. A block without its own header uses the previous one, so several blocks for one file need the path once. Blocks run in order: a later block sees the earlier ones' result. You can chain a build after it: `splice <<'EOF' ... EOF` and then `crux go build ./...` in the same command, or in the next one.

## When to use it

| Situation | Use |
| --- | --- |
| Two or more replacements, in one file or several | splice, one call |
| One small replacement in a file you just read | the Edit tool is fine; splice works too |
| Rewrite one function, method or section whole | splice with a ref header and an empty SEARCH |
| A new file | splice with the new path and an empty SEARCH, or the Write tool |
| Rename across a codebase, regex rewrites | not splice: it does exact text only |

splice doesn't need the file to have been read with the read tool, but it needs the exact old text. Take it from `seek -s Name`, `seek path:Name` or a read, not from memory.

## Headers

| Header | The old text is looked for in |
| --- | --- |
| `src/pty.rs` (or just `pty.rs` if that's unique) | the whole file |
| `src/server.go:Server.run` | that definition only |
| `main.go:104` | the innermost definition containing line 104 |
| `main.go:80-120` | those lines |

A ref header is the easy way to make a common line unique: `return nil` occurs fifty times in a file but once in `Server.close`. Use the refs that seek, recap and crux print, as they print them.

## Blocks

- **Normal**: the old text must occur exactly once in the scope. It may be part of a line, one line or many.
- **`<<<<<<< SEARCH all`**: replace every exact occurrence in the scope.
- **Empty SEARCH, ref header** (`main.go:run`): replace that whole definition, from its defining line to its end. Its doc comment stays; to change the doc comment too, use a normal block that includes it. If your new text starts at another indentation than the definition, splice moves it.
- **Empty SEARCH, path that doesn't exist**: create the file (and its folders).
- **No lines between `=======` and `>>>>>>> REPLACE`**: delete the old text; whole lines go with their line breaks. (One empty line there leaves an empty line.) To delete a definition, put it and its doc comment in the old text.

If the old text isn't there exactly, splice tries again ignoring spaces at line ends, then with all lines' indentation shifted by the same amount (the new text is shifted too). It says so in a `note:` line.

**Marker lines in the text**: if the old or new text contains lines like `=======` or `<<<<<<< SEARCH` (a test or doc about this format, a merge conflict), use longer markers for that block, all three the same length: `<<<<<<<<< SEARCH`, `=========`, `>>>>>>>>> REPLACE`.

## When a block fails

Nothing is written, every failed block is listed, and the exit code is 1. Fix the blocks and send the whole batch again (the blocks that passed weren't applied either).

- `the old text occurs 3 times` with each place as `path:line in Name`: add a line or two of context, or use one of those definitions as the header, or `SEARCH all` if you mean all of them.
- `the old text isn't in ...` with `nearest:` and the first line that differs (`file:` vs `block:`; `→` is a tab, `·` a trailing space): copy the file's line into your block.
- `but the new text is, at ...: was this block applied already?`: it was. Leave the block out.
- `has no definition X` with `did you mean:`: use one of the listed refs.

Exit code 2 means the input itself is broken (a missing marker, text outside a block); the message gives the line.

## The report

One header per file, one line per changed definition (`changed`, `added`, `removed`, with kind, name, new line range and `+added -removed` lines). Header path + `:` + name is a ref for seek or recap. Doc comments count with their definition; other changes show as `outside definitions L3-5`.

- `next: seek path:Name   its callers: its signature changed`: run it when you changed a signature, to find the call sites to update.
- `check: path:line:col: ...` (Go and JSON only): the edit broke a file that parsed before. It was written anyway; fix it next.
- `-n` checks and reports without writing. `--diff` prints each block's old and new lines.
