# planner

A terminal weekly planner / journal built with [Bubble Tea](https://github.com/charmbracelet/bubbletea).

Five day-boxes across the screen with the **selected day kept in the centre**,
the year and ISO week number up top. Each day is a section of a single markdown
file, so the whole journal is plain text you can read, grep, and sync.

## Storage

Everything lives in one markdown file (default `~/.planner/journal.md`,
override with `PLANNER_FILE`). Each day is a header section:

```markdown
# 26 June 2026

- ship the planner
- groceries

# 27 June 2026

rest
```

Empty days are dropped on save. The legacy `## 2026-06-26 (Friday)` header
style is still read, so old files keep working.

## Modes

The UI is modal, vim-style. **`Esc` steps "up" one level**; going deeper keeps
you inside a day's box. The border colour tells you where you are
(plain = days, **yellow** = normal, **green** = insert).

```
DAYS  ──(i / enter)──▶  INSERT  ──(esc)──▶  NORMAL  ──(esc / q)──▶  DAYS
                                  ▲                  │
                                  └──── i / a / o ───┘
```

- **DAYS** ("ultra normal") — move between days, jump weeks, toggle tasks.
- **NORMAL** — cursor parked in one day's box; move around, toggle tasks, or
  drop into insert. `Esc`/`q` pops back up to DAYS.
- **INSERT** — type freely. `Esc` drops to NORMAL (doesn't leave the box).

> Why `Esc` and not `Shift+Esc`? Terminals don't emit a distinct code for
> Shift+Esc, so it can't be bound. Stepping up one level per `Esc` gives the
> same two-tier behaviour reliably.

Prefer real Vim? Press `E` (from any mode) to open the day in **`vim`**, or `o`
(from DAYS) to open the whole journal positioned on the selected day.

## Keys

### DAYS (week view)
| key           | action                                  |
|---------------|-----------------------------------------|
| `j` / `k`     | next / previous day (also `h`/`l`/arrows) |
| `H` / `L`     | jump back / forward a week              |
| `t`           | jump to today                           |
| `↑` / `↓`     | select task in the focused day          |
| `space` / `x` | toggle the selected task                |
| `i` / `enter` | open the day and start typing (INSERT)  |
| `E`           | edit the day in real `vim`              |
| `o`           | open the whole journal in `vim`         |
| `q`           | quit                                    |

### INSERT (typing in a box)
| key            | action                          |
|----------------|---------------------------------|
| any text       | insert                          |
| `enter`        | new line                        |
| arrows / `home` / `end` | move cursor            |
| `esc`          | → NORMAL (stay in the box)      |
| `ctrl+c`       | save & quit                     |

### NORMAL (cursor parked in a box)
| key            | action                          |
|----------------|---------------------------------|
| `h` `j` `k` `l`| move cursor (also arrows)       |
| `0` / `$`      | line start / end                |
| `g` / `G`      | first / last line               |
| `i` `a` `A` `I`| insert (here / after / EOL / BOL) |
| `o` / `O`      | open line below / above         |
| `dd`           | delete the current line         |
| `D`            | delete to end of line           |
| `u`            | undo last change                |
| `space` / `x`  | toggle the task on this line    |
| `E`            | hand off to `vim`               |
| `esc` / `q`    | → DAYS                          |

## Tasks

**Any bullet line is a task** — you don't have to type the checkbox:

```markdown
- buy milk          ← an open task
- [ ] also open
- [x] done          ← rendered dimmed + struck through
```

Toggling an open bullet checks it off (`- buy milk` → `- [x] buy milk`); toggling
again reopens it. Toggle from DAYS (`↑`/`↓` to pick, `space`) or from NORMAL
(move to the line, `space`) — no insert mode needed. Horizontal rules (`---`)
and empty bullets are not treated as tasks.

## Develop

```sh
nix develop      # go + gopls + tools
go run .
```

## Build / install

```sh
nix build        # produces ./result/bin/planner
# or
go build -o planner .
```
