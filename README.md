# elzar

Formerly slurmtui; renamed to avoid a clash with another project.

A terminal interface for viewing Slurm jobs, their details, and account shares.

**Project status:** Progress by milestone: https://github.com/users/underwoo/projects/1

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

elzar uses Slurm's JSON interfaces; it has no plain-text output fallback.

## Download

Tagged semantic versions publish static Linux binaries for amd64 and arm64, along with a `SHA256SUMS` file, on the [GitHub Releases page](https://github.com/underwoo/elzar/releases). The repository is private, so authenticate the GitHub CLI first with `gh auth login`.

For example, download the `v0.4.0` release with `gh` and verify the assets:

```sh
gh release download v0.4.0 --repo underwoo/elzar
sha256sum -c SHA256SUMS
chmod +x elzar-linux-*
```

Or download with `curl` (using a GitHub token when the repository is private):

```sh
curl -fLO https://github.com/underwoo/elzar/releases/download/v0.4.0/elzar-linux-amd64
curl -fLO https://github.com/underwoo/elzar/releases/download/v0.4.0/SHA256SUMS
sha256sum --ignore-missing -c SHA256SUMS
chmod +x elzar-linux-amd64
```

Run the binary for your machine's architecture:

```sh
./elzar-linux-amd64
# or
./elzar-linux-arm64
```

The binaries are attached directly to the release; no Go toolchain is needed on the machine where you run them.

## Build and run

Install with `go install`:

```sh
go install github.com/underwoo/elzar/cmd/elzar@latest
```

Or clone and build from source:

```sh
git clone https://github.com/underwoo/elzar.git
cd elzar
make build
./elzar
```

`make build` writes the executable to `./elzar`. To print its build version, run:

```sh
./elzar --version
```

If Go is unavailable on the machine where you will run elzar, build for Linux amd64 on another machine with Go installed, then copy the binary to the Slurm environment:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 make build
```

The binary still needs the Slurm commands and cluster access described above at runtime.

## Views and keyboard shortcuts

The **Queue** view shows active jobs. **Recent** shows jobs that completed within the configured recent window. Use the arrow keys or `j`/`k` to move through the list.

| Key | Action |
| --- | --- |
| `Tab`, `Shift+Tab` | Cycle through views |
| `Ctrl+T`, then `1`/`2`/`3`/`4` | Jump to Queue, Recent, Accounts, or Files; the prefix is configurable |
| `Ctrl+T`, then `s` | Open the status and error message log |
| `↑`/`↓`, `j`/`k` | Move the selection or scroll details |
| `Space`, `Ctrl+V`, `Ctrl+F`, `PgDn` | Page down |
| `b`, `Ctrl+B`, `Alt+V`, `PgUp` | Page up |
| `N` then motion | Repeat a motion N times (for example `5j`, `3 Space`) |
| `gg`, `1G`, `Home`, `<`, `Alt+<` | Jump to the top |
| `G`, `End`, `>`, `Alt+>` | Jump to the bottom |
| `Enter` | Open details for the selected job or account |
| `/` | Open the live regular-expression filter (Enter keeps it; Esc clears it) |
| `s` | Sort by a visible column; selecting the current column flips direction |
| `c` | Choose visible columns; use Space to toggle, `J`/`K` to reorder, and `w` to save |
| `x` | Cancel the selected job after confirmation |
| `h` | Hold a pending job, or release a user/admin-held job, after confirmation |
| `R` | Requeue the selected job after confirmation |
| `e` | Edit a job's time limit, name, and comment, then confirm the changes |
| `o`, `O` | Open the selected job's standard output or standard error in `$PAGER` (built-in plugins) |
| `Esc` | Return from job details to the list, or clear an active filter |
| `a` | Show or hide the raw All fields section in job details |
| `r` | Refresh job lists, or Accounts when that view is active |
| `u` | Toggle between your jobs and all visible users' jobs |
| `?` | Show or hide the key help overlay |
| `q`, `Ctrl+C` | Quit |

Job details include summary, requested and allocated resources, timing, paths, accounting steps when available, and reason/dependency information. Active jobs are queried with `scontrol`; Recent jobs use `sacct`.

The message log keeps the latest 200 status and error messages, newest first. Repeated consecutive messages are grouped with a `×N` count. Use the same navigation motions to scroll it; `Esc`, `q`, or the tab prefix followed by `s` closes it.

The filter uses Go's RE2 regular-expression syntax. Lowercase patterns match without regard to case; adding an uppercase letter makes the pattern case-sensitive. The filter applies to the rendered values of visible columns in all three views.

On narrow terminals, tables hide overflow columns from the right and show
`+N cols` in the header. Queue and Recent retain JobID; Accounts retains Account,
provided you have configured that column as visible. Its full header/value width
is reserved (capped to fit the terminal and overflow badge), and retained columns
stay in configured order. Widening the terminal
restores the columns. Regex filters still match all configured columns, including
those temporarily hidden by width.

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

Press `Enter` to inspect an account’s lineage, shares and decayed usage, your
FairShare and Level, and allowed QOS limits. MaxWall is shown in minutes;
`—` means unset and `∞` means unlimited. Use the usual scrolling motions,
then `Esc` to return to the selected account. Non-member accounts show their
lineage without claiming user-specific QOS access.

Account detail also lists partitions matching your association's account and QOS,
with their configured MaxTime/DefaultTime (minutes), MaxNodes, and state. Queries
run once per association cluster during the Accounts refresh; a failed cluster
shows unavailable access and a message-log diagnostic. This is an account/QOS
check, not a guarantee that a job can run: other Slurm policies and resources
still apply. Account lists include descendants, and explicit allow lists take
precedence over deny lists, following [Slurm's partition rules](https://slurm.schedmd.com/slurm.conf.html).

## Files and submit

Use `Ctrl+T` then `4` to open Files, or cycle through views with `Tab` (`Shift+Tab` cycles backward). Files starts in the directory from which you launched elzar and can navigate up to any parent directory.

The left pane shows a collapsible directory tree; the right pane previews the selected entry with a `size · mtime` header (on terminals narrower than 100 columns, the preview stacks below or hides). Directories sort first, followed by entries in alphabetical order. Symlink targets are displayed alongside the entry; symlinked directories expand only when you open them explicitly, preventing symlink loops. Each directory listing is capped at 5,000 entries with an `"N more not shown"` marker.

Previews read **regular files only** (never opening FIFOs, sockets, or device nodes) and read at most **64 KiB**. A file containing a NUL byte in its first 8 KiB is shown as `"binary file, N bytes"`. Regular files that start with `#!` on line 1 and contain at least one `#SBATCH` directive before the first command line are marked as batch scripts in the tree.

### Files view shortcuts

| Key | Action |
| --- | --- |
| `↑`/`↓`, `j`/`k` | Move selection through the tree |
| `Space`, `Ctrl+V`, `Ctrl+F`, `PgDn` | Page down |
| `b`, `Ctrl+B`, `Alt+V`, `PgUp` | Page up |
| `N` then motion, `gg`/`1G`, `G` | Counted motions, jump to top or bottom |
| `Enter` | Enter a directory as the tree root |
| `l`, `→` | Expand or collapse a directory in place |
| `h`, `←` | Collapse the current directory, move to its parent entry, or go to the parent directory |
| `Backspace` | Go up to the parent directory |
| `.` | Toggle hidden (dotfile) entries |
| `/` | Filter entries in the current directory (`Enter` keeps, `Esc` clears) |
| `r` | Re-read the current directory |
| `v` | View the selected file in `$PAGER` (default `less`) |
| `e` | Edit the selected file in `$EDITOR` (default `vi`), keeping selection and tree state on return |
| `S` | Open the sbatch submit form for the selected batch script |

### Submit form (`S`)

Pressing `S` on a batch script opens the submit form. **elzar never opens a script for writing and never modifies your script file** — edit scripts externally or with `e` (`$EDITOR`). Heterogeneous job scripts (`#SBATCH hetjob` or `#SBATCH packjob`) are refused with `"heterogeneous job scripts aren't supported yet"`.

#### Precedence and source labels

The form inspects `#SBATCH` directives (up to the first non-comment, non-blank line) and `SBATCH_*` input environment variables, displaying each field's effective value alongside a dim source label:

1. **`form`** — edited in the submit form (highest precedence; passed on the `sbatch` command line).
2. **`env SBATCH_<VAR>`** — set in the environment (overrides `#SBATCH` in the script). Only the `SBATCH_*` input variables documented in `man sbatch` (Slurm 25.11) apply (`SBATCH_JOB_NAME`, `SBATCH_ACCOUNT`, `SBATCH_PARTITION`, `SBATCH_QOS`, `SBATCH_TIMELIMIT`, `SBATCH_MEM_PER_NODE`, `SBATCH_GRES`, `SBATCH_GPUS`, `SBATCH_ARRAY_INX`, `SBATCH_OUTPUT`, `SBATCH_ERROR`; `nodes`, `ntasks`, `cpus-per-task`, and `chdir` have no sbatch input environment variable).
3. **`script:<line>`** — set by a `#SBATCH` directive at line `<line>` in the script.
4. **`default`** — unset in the form, environment, and script (Slurm or association defaults apply).

#### Edited-only `argv` and empty-edit reset

Because `sbatch` reads `#SBATCH` lines and `SBATCH_*` environment variables itself, **elzar passes only the fields you edit in the form**:

```text
sbatch --parsable [--test-only] <only edited form fields> [extra sbatch options] <abs-script-path> [script arguments]
```

If you submit without editing any fields, elzar invokes `sbatch --parsable /abs/path/to/script.sh`, preserving `CLI > SBATCH_* env > #SBATCH` precedence by construction. Directives outside the form's fixed field set are listed read-only under **Passed through by sbatch**.

Because the `sbatch` CLI cannot unset an option already defined in `#SBATCH` or `SBATCH_*`, clearing a field to empty in the form resets it to its underlying `script:<line>`, `env SBATCH_X`, or `default` value and source (with a notice when a script or environment value is still in effect: `"can't unset from the form; edit the script"`). Press `Ctrl+R` on any field to reset it to its unedited script/env/default state.

#### Working directory

`sbatch` runs with its working directory set to **the script's directory** (as if you ran `cd <script-dir> && sbatch ...`), unless `--chdir` (`-D`) is set in the script or form (relative `--chdir` paths resolve relative to the script's directory). The effective working directory and a live `argv` preview are shown at the bottom of the form.

#### Pickers, limit warnings, dry run, and submission

| Key | Action |
| --- | --- |
| `↑`/`↓`, `Tab`/`Shift+Tab` | Move between form fields |
| `Ctrl+P` | Open the list picker on **Account**, **Partition**, or **QOS** (populated from your M3 associations, allowed partitions, and allowed QOS, with your default account/QOS listed first) |
| `Ctrl+R` | Reset the focused field to its script, environment, or default value |
| `t`, `Ctrl+T` | Run a `--test-only` dry run (`Ctrl+T` from any field, or `t` on the submit/dry-run action row) and show `sbatch`'s start-time estimate (or error) inline in the form |
| `Enter` | Move to the next field, or on the submit action row review the full `sbatch` command in the confirmation dialog (`y` to submit) |
| `Esc` | Cancel and return to the Files view |

Non-blocking warnings (yellow) appear beneath a field and in the confirmation dialog when a value conflicts with your loaded M3 association, partition, or QOS data — for example, an account not in your associations, a partition or QOS not allowed for that account, a time limit exceeding QOS `MaxWall` or partition `MaxTime`, or a node count exceeding partition `MaxNodes` or QOS `MaxTRESPerJob`. Warnings never block dry runs or submission (`sbatch` is the final authority).

When a real submission succeeds, elzar logs `Submitted job <id>` to the status bar and `Ctrl+T s`, switches to **Queue**, refreshes, and selects the newly submitted job. If `sbatch` fails, the form stays open with `sbatch`'s error message so you can adjust options without re-entering your edits.


## Options

| Option | Description | Default |
| --- | --- | --- |
| `--refresh <duration>` | Queue refresh interval | `15s` |
| `--window <duration>` | How far back Recent includes completed jobs | `1h` |
| `--all` | Start with all visible users' jobs | Your jobs (`me`) |
| `--version` | Print the build version and exit | — |
| `plugins` | List discovered plugins and their validation status, exiting non-zero on any error | — |

Durations use Go duration syntax, such as `30s`, `5m`, or `2h`. Examples:

```sh
./elzar --refresh 30s
./elzar --window 12h
./elzar --all
./elzar plugins
```

The `u` key changes the current scope while the program is running.

## Configuration

Configuration is read from `elzar/config.toml` under `$XDG_CONFIG_HOME`. If `XDG_CONFIG_HOME` is unset, elzar uses the platform's user config directory (on Linux, usually `~/.config`). For example, the Linux default path is `~/.config/elzar/config.toml`.

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

The optional `[plugins]` table configures plugin discovery and timeouts:

```toml
[plugins]
enabled = true
site_dir = "/etc/elzar/plugins"
disabled = []
timeout = "5s"
```

## Plugins

elzar supports two kinds of declarative YAML plugins (`*.yaml` or `*.yml`):

- **`command` plugins** bind a keyboard shortcut to an external command, filling in job or account fields with Go `text/template` expressions.
- **`exec` plugins** run an executable that speaks JSON protocol v1 over stdin/stdout to contribute custom table columns and Job Detail sections.

Sample manifests and a working `sh` + `jq` exec plugin live in [`examples/plugins/`](examples/plugins/).

### Discovery and precedence

Plugins are discovered across three layers, from highest precedence to lowest:

1. **User directory:** `$XDG_CONFIG_HOME/elzar/plugins/` (or `~/.config/elzar/plugins/` when `XDG_CONFIG_HOME` is unset)
2. **Site directory:** `/etc/elzar/plugins/` (configurable via `[plugins] site_dir` in `config.toml`)
3. **Built-in plugins:** embedded in the `elzar` binary (`stdout` on `o` and `stderr` on `O`, which open the job's standard output or standard error in `{{env "PAGER" "less"}}`)

A plugin in a higher layer with the same `name` replaces the lower-layer plugin. Duplicate plugin `name` values within the same layer invalidate that name in that layer. Missing directories are ignored.

Set `[plugins] disabled = ["name", ...]` in `config.toml` to disable specific plugins (including built-ins), or `enabled = false` to disable all plugins.

### Manifest formats

Each manifest file must contain a single YAML document. Unknown YAML keys are rejected as validation errors. If `command` is a relative path containing a path separator (such as `./job-info.sh`), it is resolved relative to the directory containing the manifest.

#### `command` plugin manifest

```yaml
name: ssh-first-node
description: SSH to the job's first allocated node
type: command
key: S
scopes: [queue, detail]
command: ssh
args: ['{{.FirstNode}}']
mode: foreground
confirm: true
```

| Field | Required | Description |
| --- | --- | --- |
| `name` | Yes | Unique plugin identifier |
| `description` | Yes | Shown in the `?` help overlay under **Plugins** |
| `type` | Yes | `"command"` |
| `key` | Yes | Single printable key, `ctrl+<a-z>`, or `alt+<key>` (multi-key chords are not allowed) |
| `scopes` | Yes | Non-empty list from `queue`, `recent`, `detail` (Job Detail), and `accounts` (both the Accounts list and Account Detail) |
| `command` | Yes | Executable name or path (rendered as a template) |
| `args` | No | List of argument templates; each element is rendered separately and passed directly in `argv` without a shell |
| `mode` | Yes | `"foreground"` (suspends the TUI while the command runs, for pagers, `ssh`, or editors) or `"background"` (runs asynchronously; the first line of output appears on the status bar and full output is logged to `Ctrl+T s`) |
| `confirm` | No | When `true`, prompts for confirmation (`y`) showing the rendered `argv` before running |

Because elzar never invokes a shell implicitly, arguments containing spaces or quotes stay a single `argv` element. If you intentionally want a shell pipeline, invoke `sh -c` explicitly and quote interpolated fields with `shquote`:

```yaml
name: scontrol-show-job
description: Show scontrol job details in pager
type: command
key: i
scopes: [queue, detail]
command: sh
args:
  - -c
  - 'scontrol show job {{shquote .JobID}} | {{env "PAGER" "less"}}'
mode: foreground
confirm: false
```

#### `exec` plugin manifest

```yaml
name: job-info
description: Example exec plugin adding a WorkDir column and an Environment detail section (requires POSIX sh and jq)
type: exec
command: ./job-info.sh
args: []
provides: [columns, detail]
```

| Field | Required | Description |
| --- | --- | --- |
| `name` | Yes | Unique plugin identifier |
| `description` | Yes | Short description of the plugin |
| `type` | Yes | `"exec"` |
| `command` | Yes | Executable name or path |
| `args` | No | Optional static/environment argument templates |
| `provides` | Yes | Non-empty list containing `"columns"`, `"detail"`, or both |

`exec` plugins cannot specify `key`, `scopes`, `mode`, or `confirm`.

### Template fields and functions

`command` and `args` templates use Go's `text/template` with `missingkey=error`. Referencing a field that is unavailable in the current scope (or empty on the selected job, such as `StdOut` or `FirstNode` on a pending job) fails cleanly with a status message instead of substituting an empty string.

**Available fields in `queue`, `recent`, and `detail` scopes:**

| Field | Description |
| --- | --- |
| `.JobID` | Display job ID |
| `.Name` | Job name |
| `.User` | Job owner username |
| `.Account` | Account name |
| `.Partition` | Partition name |
| `.QOS` | QOS name |
| `.State` | Comma-joined job states (e.g. `RUNNING`) |
| `.Cluster` | Cluster name |
| `.NodeList` | Raw Slurm nodelist expression |
| `.FirstNode` | First node expanded from `.NodeList` (e.g. `node001` from `node[001-020]`) |
| `.WorkDir` | Working directory |
| `.StdOut` | Resolved standard output path |
| `.StdErr` | Resolved standard error path |
| `.Comment` | Job comment |

**Available fields in `accounts` scope (Accounts list and Account Detail):**

| Field | Description |
| --- | --- |
| `.Cluster` | Cluster name |
| `.Account` | Account name |
| `.Parent` | Parent account name |

**Template functions:**

- `{{shquote .Field}}` — wraps a value in single quotes with embedded single quotes escaped (`'...'`), safe for passing inside `sh -c` scripts.
- `{{env "VAR" "fallback"}}` — returns the environment variable `VAR` if set and non-empty, or `"fallback"` otherwise.
- `{{default "fallback" .Field}}` — returns `"fallback"` when `.Field` is empty (note: `.Field` must still be a defined key in the map).

### Exec plugin protocol v1

An `exec` plugin is invoked as a short-lived child process for each operation. Standard output is capped at **1 MiB** per call, and each call is bounded by `[plugins] timeout` (default `5s`). Unknown JSON fields in responses are ignored.

#### 1. `describe` (at startup / `elzar plugins`)

elzar runs `<command> [args...] describe` with empty stdin. The plugin must write a single JSON object to stdout declaring protocol `1` and the columns or detail sections it provides:

```json
{
  "protocol": 1,
  "columns": [
    {
      "id": "workdir",
      "header": "WorkDir",
      "width": 24,
      "tabs": ["queue", "recent"]
    }
  ],
  "sections": [
    {
      "id": "paths",
      "title": "Job Paths (Plugin)"
    }
  ]
}
```

- Column IDs register in elzar as `plugin.<name>.<id>` (for example, `plugin.job-info.workdir`). They are hidden by default and can be toggled on, reordered, and saved to `config.toml` from the `c` column picker, as well as sorted (`s`) and filtered (`/`).
- Detail sections are appended after built-in sections and before `All fields` (`a`).

#### 2. `columns` (once per refresh per tab, batched)

After each successful Queue or Recent refresh, elzar runs `<command> [args...]` and passes a single batched JSON request on stdin:

```json
{
  "protocol": 1,
  "op": "columns",
  "tab": "queue",
  "jobs": [
    {
      "JobID": "101",
      "Name": "train-llm",
      "User": "alice",
      "Account": "ml",
      "Partition": "gpu",
      "QOS": "normal",
      "State": "RUNNING",
      "NodeList": "gpu-01",
      "FirstNode": "gpu-01",
      "WorkDir": "/scratch/alice/train"
    }
  ]
}
```

The plugin responds on stdout with cell values keyed by `JobID` and column `id`:

```json
{
  "values": {
    "101": {
      "workdir": "/scratch/alice/train"
    }
  }
}
```

While the first call is in flight, plugin cells display `…`. If a call fails and no prior value exists, cells display `—`; if prior values exist from an earlier refresh, they are preserved across failed calls.

#### 3. `detail` (when opening Job Detail)

When a user opens Job Detail (`Enter`), elzar runs `<command> [args...]` with the job's template fields plus the raw Slurm JSON object under `"raw"`:

```json
{
  "protocol": 1,
  "op": "detail",
  "job": {
    "JobID": "101",
    "Name": "train-llm",
    "WorkDir": "/scratch/alice/train",
    "StdOut": "/scratch/alice/train/slurm-101.out",
    "StdErr": "/scratch/alice/train/slurm-101.err",
    "raw": {
      "job_id": 101,
      "name": "train-llm"
    }
  }
}
```

The plugin responds on stdout with lines for each section ID:

```json
{
  "sections": {
    "paths": [
      "WorkDir: /scratch/alice/train",
      "StdOut:  /scratch/alice/train/slurm-101.out",
      "StdErr:  /scratch/alice/train/slurm-101.err"
    ]
  }
}
```

While in flight, the section shows `loading…`. If the call fails, the error is shown inline as a single line (`error: …`) and logged to `Ctrl+T s`.

### Isolation, circuit breaker, and file permissions

- **File permissions:** Every manifest file (`*.yaml`/`*.yml`) and resolved plugin executable must be a regular file that is **not writable by group or others** (mode `0022` bits must be clear, e.g. `0600`/`0644` for manifests and `0700`/`0755` for executables). Files failing this check are skipped and logged.
- **Key collisions:** Built-in key bindings always win. A command plugin whose key collides with a built-in binding (or with an earlier command plugin in the same scope) is disabled and logged to `Ctrl+T s`.
- **Circuit breaker:** If an `exec` plugin fails **3 consecutive calls** (non-zero exit, timeout, invalid JSON, or output exceeding 1 MiB), it is disabled for the remainder of the session and logged once to `Ctrl+T s`.

### Diagnostics (`elzar plugins`)

Run `elzar plugins` to inspect all discovered plugins across layers, run `describe` against `exec` plugins, and verify permissions and key bindings:

```sh
./elzar plugins
```

Example output:

```text
NAME               LAYER    PATH                                            TYPE     DETAILS                           STATUS
job-info           user     /home/alice/.config/elzar/plugins/job-info.yaml exec     provides=columns,detail           ok
scontrol-show-job  user     /home/alice/.config/elzar/plugins/scontrol.yaml command  key=i scopes=queue,detail         ok
stderr             builtin  builtins/stderr.yaml                            command  key=O scopes=queue,recent,detail  ok
stdout             builtin  builtins/stdout.yaml                            command  key=o scopes=queue,recent,detail  ok
```

Each plugin reports `ok`, `disabled: …`, or `error: …`. `elzar plugins` exits with status `0` when all discovered plugins are `ok` or `disabled`, and exits non-zero (`1`) if any plugin has an error (or `2` on invalid CLI flags/config), making it suitable for site configuration checks and CI.

## How elzar is built

elzar is written entirely by AI agents, with no human-written application code. A human owner directs the project and tests releases on real Slurm clusters. The agents collaborate in a Buzz channel.

| Role | Participant | Model | Responsibilities |
| --- | --- | --- | --- |
| Owner | underwoo (human) | — | Sets product direction and requirements; resolves disagreements; tests every release on live Slurm clusters; provides data samples; approves milestone plans; manages infrastructure and credentials. |
| Planner | Scout | Claude Opus 5.5 (`claude-opus-5-5`) | Turns requests into scoped plans and GitHub issues; checks PRs against the plan; independently verifies release checksums and version output before announcing releases. |
| Implementer | Patch | OpenAI GPT-6 (Codex) | Writes code, tests, and documentation; opens PRs; merges after reviewer approval and passing CI; tags releases. |
| Implementer | Anvil | Google Gemini 4 Argon (Antigravity) | Writes code, tests, and documentation; opens PRs; merges after reviewer approval and passing CI; tags releases. |
| Reviewer | Warden | Google Gemini 4 Argon (Antigravity) | Reviews every PR against the plan and runs build, test, and lint checks locally before approving. |

The workflow is: request → plan and issue → pull request → review → CI → merge → tagged release → owner testing on real clusters. Bugs and requests from that testing become new issues. Sample data is scrubbed before it is committed to the repository.

## License

Licensed under [Apache-2.0](LICENSE).
