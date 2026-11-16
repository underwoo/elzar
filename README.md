# slurmtui

A terminal interface for viewing Slurm jobs, their details, and account shares.

## Requirements

- Slurm **25.11 or newer**, with JSON output available from `squeue`, `sacct`, `scontrol`, `sshare`, and `sacctmgr`.
- At runtime, these Slurm commands must be on `PATH` and configured to access the cluster:
  - `squeue --json`
  - `sacct --json`
  - `scontrol show job --json`
  - `sshare --json -l -U -M all`
  - `sacctmgr --json show assoc user=<your-user>`
  - `sacctmgr --json show qos`
- Go **1.26 or newer** to build from source.

slurmtui uses Slurm's JSON interfaces; it has no plain-text output fallback.

## Download

Tagged semantic versions publish static Linux binaries for amd64 and arm64, along with a `SHA256SUMS` file, on the [GitHub Releases page](https://github.com/underwoo/slurmtui/releases). The repository is private, so authenticate the GitHub CLI first with `gh auth login`.

For example, download the `v0.1.0` release and verify the assets:

```sh
gh release download v0.1.0 --repo underwoo/slurmtui
sha256sum -c SHA256SUMS
chmod +x slurmtui-linux-*
```

Run the binary for your machine's architecture:

```sh
./slurmtui-linux-amd64
# or
./slurmtui-linux-arm64
```

The binaries are attached directly to the release; no Go toolchain is needed on the machine where you run them.

## Build and run

```sh
git clone https://github.com/underwoo/slurmtui.git
cd slurmtui
make build
./slurmtui
```

`make build` writes the executable to `./slurmtui`. To print its build version, run:

```sh
./slurmtui --version
```

If Go is unavailable on the machine where you will run slurmtui, build for Linux amd64 on another machine with Go installed, then copy the binary to the Slurm environment:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 make build
```

The binary still needs the Slurm commands and cluster access described above at runtime.

## Views and keyboard shortcuts

The **Queue** view shows active jobs. **Recent** shows jobs that completed within the configured recent window. Use the arrow keys or `j`/`k` to move through the list.

| Key | Action |
| --- | --- |
| `Tab`, `Shift+Tab` | Cycle through views |
| `Ctrl+T`, then `1`/`2`/`3` | Jump to Queue, Recent, or Accounts; the prefix is configurable |
| `Ctrl+T`, then `s` | Open the status and error message log |
| `↑`/`↓`, `j`/`k` | Move the selection or scroll details |
| `Space`, `Ctrl+V`, `Ctrl+F`, `PgDn` | Page down |
| `b`, `Ctrl+B`, `Alt+V`, `PgUp` | Page up |
| `N` then motion | Repeat a motion N times (for example `5j`, `3 Space`) |
| `gg`, `1G`, `Home`, `<`, `Alt+<` | Jump to the top |
| `G`, `End`, `>`, `Alt+>` | Jump to the bottom |
| `Enter` | Open details for the selected job |
| `/` | Open the live regular-expression filter (Enter keeps it; Esc clears it) |
| `s` | Sort by a visible column; selecting the current column flips direction |
| `c` | Choose visible columns; use Space to toggle, `J`/`K` to reorder, and `w` to save |
| `x` | Cancel the selected job after confirmation |
| `h` | Hold a pending job, or release a user/admin-held job, after confirmation |
| `R` | Requeue the selected job after confirmation |
| `e` | Edit a job's time limit, name, and comment, then confirm the changes |
| `Esc` | Return from job details to the list, or clear an active filter |
| `a` | Show or hide the raw All fields section in job details |
| `r` | Refresh job lists, or Accounts when that view is active |
| `u` | Toggle between your jobs and all visible users' jobs |
| `?` | Show or hide the key help overlay |
| `q`, `Ctrl+C` | Quit |

Job details include summary, requested and allocated resources, timing, paths, accounting steps when available, and reason/dependency information. Active jobs are queried with `scontrol`; Recent jobs use `sacct`.

The message log keeps the latest 200 status and error messages, newest first. Repeated consecutive messages are grouped with a `×N` count. Use the same navigation motions to scroll it; `Esc`, `q`, or the tab prefix followed by `s` closes it.

The filter uses Go's RE2 regular-expression syntax. Lowercase patterns match without regard to case; adding an uppercase letter makes the pattern case-sensitive. The filter applies to the rendered values of visible columns in all three views.

## Accounts

Use `Ctrl+T` then `3` to open Accounts, or cycle through Queue, Recent, and
Accounts with `Tab` (`Shift+Tab` cycles backward). Accounts shows every account
across all clusters, with one row per cluster/account pair. `You` marks your
accounts as `default` or `member`; `/member|default` filters to those rows.

`RawShares` is the account's allocation target. Usage is the current decayed
`sshare` usage, rather than usage over a reporting period. FairShare shows your
user factor for accounts you belong to and is blank for other accounts.

Press `M` for the cluster selector. Space toggles All or individual clusters,
Enter applies, and Esc cancels. An empty selection means All. Cluster selection
combines with the regex filter, and the status line shows the selected clusters.
The selector changes this session only; set `[accounts] clusters` below to
choose the initial selection.

Filtering, sorting, column selection (`c`, then `w` to save), counted motions,
and paging work as in the job views. Accounts refreshes independently every
five minutes, or immediately with `r`. Failed clusters are logged in `Ctrl+T s`;
previous rows for those clusters remain visible while successful clusters update.
Job actions do not apply in Accounts.

## Options

| Option | Description | Default |
| --- | --- | --- |
| `--refresh <duration>` | Queue refresh interval | `15s` |
| `--window <duration>` | How far back Recent includes completed jobs | `1h` |
| `--all` | Start with all visible users' jobs | Your jobs (`me`) |
| `--version` | Print the build version and exit | — |

Durations use Go duration syntax, such as `30s`, `5m`, or `2h`. Examples:

```sh
./slurmtui --refresh 30s
./slurmtui --window 12h
./slurmtui --all
```

The `u` key changes the current scope while the program is running.

## Configuration

Configuration is read from `slurmtui/config.toml` under `$XDG_CONFIG_HOME`. If `XDG_CONFIG_HOME` is unset, slurmtui uses the platform's user config directory (on Linux, usually `~/.config`). For example, the Linux default path is `~/.config/slurmtui/config.toml`.

```toml
refresh_interval = "15s"
recent_window = "1h"
default_scope = "me"

[keys]
tab_prefix = "ctrl+t"

[columns]
queue = ["job_id", "name", "user", "account", "partition", "qos", "state", "elapsed_limit", "nodes", "node_reason"]
recent = ["job_id", "name", "user", "account", "partition", "qos", "state", "elapsed_limit", "nodes", "node_reason", "end"]
```

`default_scope` can be `"me"` or `"all"`. If the file is missing, these defaults apply. Invalid values produce an error at startup. Command-line options override the file; `--all` selects the all-users scope.

The optional `[keys]` table sets the key used before a digit to jump directly to a view. `tab_prefix` accepts a Bubble Tea key string such as `"ctrl+t"`, `"ctrl+y"`, or `"alt+x"`. The default is `ctrl+t`.

The optional `[columns]` table stores visible column IDs in display order. The column picker updates only this table and preserves the rest of the file. Unknown IDs are skipped with a status-line warning.

The optional `[accounts]` table configures Accounts independently of job views:

```toml
[accounts]
refresh_interval = "5m"
clusters = "all" # or a comma-separated subset, such as "c5,c6"
```

The column picker saves Accounts columns under `[columns] accounts`, preserving
job column choices and the other configuration tables.
