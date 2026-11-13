<div align="center">

# splice

Checked, batched edits: exact replacements in many files, all or nothing.

[![Go](https://img.shields.io/github/go-mod/go-version/Vaspyyy/splice?style=flat-square&labelColor=1e1e2e&color=89b4fa&logo=go&logoColor=89b4fa)](go.mod)
[![Release](https://img.shields.io/github/v/tag/Vaspyyy/splice?style=flat-square&labelColor=1e1e2e&color=cba6f7&label=release)](https://github.com/Vaspyyy/splice/tags)
[![License](https://img.shields.io/github/license/Vaspyyy/splice?style=flat-square&labelColor=1e1e2e&color=a6e3a1)](LICENSE)

</div>

splice applies a batch of SEARCH/REPLACE blocks to files. It checks every block before it writes anything, refuses a block whose old text is missing or occurs more than once, says where the nearest text differs or lists every occurrence, and reports which functions, types and sections changed.

It was written for AI coding agents. Agents often edit files with a few lines of inline Python (`assert old in s; open(p, "w").write(s.replace(old, new))`), because it lets them make several replacements in several files in one command. That works until a check is forgotten, a mismatch says only `AssertionError`, or a failure halfway leaves half the files written. splice keeps the batch and fixes those. It is part of the same family as [glint](https://github.com/Vaspyyy/glint), [seek](https://github.com/Vaspyyy/seek), [recap](https://github.com/Vaspyyy/recap) and [crux](https://github.com/Vaspyyy/crux): a block's header can be a ref those tools print (`main.go:run`).

## Install

```sh
go install github.com/Vaspyyy/splice@latest
```

You'll need Go 1.26 or newer (older versions fetch the right toolchain on their own). git is optional: in a repository, paths are printed relative to its root and a file can be named by the end of its path. splice is developed on Linux and should work on macOS and the BSDs; Windows isn't supported.

## Usage

```sh
splice [flags] [EDITS...]
```

splice reads blocks from standard input, or from the files named (`-` is standard input). In a shell, a quoted heredoc keeps the text exactly as written:

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

Each block is a header line, then the old text between `<<<<<<< SEARCH` and `=======`, then the new text up to `>>>>>>> REPLACE`. A block without a header line of its own uses the one before it. Blank lines and Markdown fence lines between blocks are ignored; any other text there is an error, since it usually means a header or marker went missing.

### Headers

| Header | The old text is looked for in |
| --- | --- |
| `src/pty.rs` | the whole file |
| `src/server.go:Server.run` | that definition |
| `main.go:104` | the innermost definition containing line 104 (outside definitions: the whole file) |
| `main.go:80-120` | those lines |

A path can be relative to the repository root or the working directory, absolute, or the unique end of a path git sees (`pty.rs`). When a relative path exists both under the root and under the working directory, the one under the root wins, as in the other tools; a new file goes under the root too when its folder exists there. A name can be the end of a qualified one (`run` for `Server.run`) and takes `#2` for the second of two definitions with the same name. A name that fits several definitions, or none, fails the block and lists the candidates.

### What a block does

- **Replace once.** The old text must occur exactly once in its scope. Overlapping occurrences count (`aa` occurs twice in `aaa`), and old text that starts with spaces or tabs matches only where that indentation starts, not inside a deeper one. If it isn't there as written, splice tries again ignoring spaces at the ends of lines, then with every line's indentation shifted by the same amount (the new text is shifted with it, so a block written without its indentation lands correctly). A tolerant match is noted in the report. Old text that is only blank lines is refused: it would match almost anywhere.
- **Replace every occurrence** with `<<<<<<< SEARCH all`: every exact occurrence in the scope, at least one.
- **Delete lines**: no lines at all between `=======` and `>>>>>>> REPLACE`. Old text that is whole lines goes with its line breaks; one empty line there leaves an empty line instead.
- **Replace a whole definition**: empty old text and a header that names a definition (by name, or by a line inside it). The defining line through its last line is replaced; the doc comment above it and the blank lines it ends in stay. The new text is moved to the definition's indentation if its first line has another.
- **Create a file**: empty old text and a path that doesn't exist. Missing folders are created.

A block whose old text contains marker lines (a test or a doc about this format) uses longer markers: `<<<<<<<<< SEARCH`, `=========` and `>>>>>>>>> REPLACE`, all of the same length. Inside it, shorter markers are text.

### All or nothing

Blocks run in order, so a later block sees the earlier ones' result, and several blocks can edit one file. splice applies them all in memory first. If one fails, it writes nothing and reports every failed block:

```
splice: 1 of 3 blocks failed, nothing written
block 2 (src/app.ts, line 9): the old text occurs 3 times
  src/app.ts:41 in App.render
  src/app.ts:88 in App.update
  src/app.ts:130 in helper
  make it unique with more lines or a header like src/app.ts:App.render, or use "SEARCH all" to replace every one
the other 2 blocks would apply
```

When the old text isn't there, splice shows the nearest lines and the first one that differs, with tabs as `→` and spaces at the end of a line as `·`. If the new text is already there, it asks whether the block was applied already.

Every changed file is first written to a temporary file next to it, and only when all of them are written are they renamed into place. So a file that changed on disk while splice ran, or a folder splice can't write to, stops the whole batch with nothing written. Modes are kept (setuid and setgid bits too), a symlink stays a symlink, and a file reached two ways (through a symlinked folder, or a hard link) is edited as one file. In a file with mixed line endings, lines that end in `\r\n` keep it.

### The report

One header per file and one line per changed definition, so the path and the name join into a ref other tools take (`src/server.go:Server.run`). A definition's doc comment counts as part of it. Other changed lines show as `outside definitions L3-5`. A definition whose signature line changed gets a `next:` line with the seek command that lists its callers. A Go or JSON file that parsed before the edit and doesn't after gets a `check:` line with the error's location; it is written anyway, since the next block or command may fix it.

### Flags

| Flag | |
| --- | --- |
| `-n`, `--dry-run` | check and report, write nothing |
| `--diff` | also print each block's old and new lines |
| `-A` | plain output without colors (the default when not on a terminal) |
| `-h`, `--help` | help |
| `-V`, `--version` | version |

Exit code: 0 when every block applied, 1 when a block failed (nothing was written), 2 for bad input.

## For coding agents

`skill/SKILL.md` is an agent skill that says when to use splice and how. Link the folder into your agent's skills folder, for Claude Code `ln -s "$PWD/skill" ~/.claude/skills/splice`.

The design, with the transcript numbers behind it, is in [docs/design.md](docs/design.md).

## License

GPL-3.0, see [LICENSE](LICENSE).
