package submit

import (
	"fmt"
	"strings"
)

// ParseScript parses `#SBATCH` (and legacy `#SLURM`) directive lines from a
// batch script up to the first non-comment, non-blank line, matching Slurm
// 25.11 `src/sbatch/opt.c:565-661` (`_opt_batch_script`).
//
// Rules verified against Slurm 25.11:
//   - Directives are recognized only when the line starts at column 0 with
//     `#SBATCH` or `#SLURM` (`opt.c:591-594`: `!xstrncmp(line, magic_word1, ...)`).
//   - Scanning stops at the first line whose first non-whitespace character is
//     not `#` (`opt.c:603-615`). Blank lines and ordinary `#` comment lines
//     before any command line are skipped.
//   - If the directive body begins with `hetjob` or `packjob` (case-insensitive,
//     followed by end-of-line or whitespace; `opt.c:624-630`), ParseScript
//     returns ErrHetJob ("heterogeneous job scripts aren't supported yet").
//   - Each directive line is tokenized with Tokenize (`get_argument` in
//     `opt.c:473-556`).
//   - Known fixed fields are recorded with their 1-based line number (later
//     directives override earlier ones, matching getopt_long in `_set_options`,
//     `opt.c:663-679`). Directives outside the fixed field set are preserved in
//     Passthrough in script order.
func ParseScript(content string) (ParsedScript, error) {
	result := ParsedScript{
		Fields: make(map[FieldID]ScriptValue),
	}

	lines := strings.Split(content, "\n")
	for idx, rawLine := range lines {
		lineno := idx + 1
		line := strings.TrimSuffix(rawLine, "\r")

		var rest string
		switch {
		case strings.HasPrefix(line, "#SBATCH") && (len(line) == len("#SBATCH") || isASCIIWhitespace(line[len("#SBATCH")]) || line[len("#SBATCH")] == '-'):
			rest = line[len("#SBATCH"):]
		case strings.HasPrefix(line, "#SLURM") && (len(line) == len("#SLURM") || isASCIIWhitespace(line[len("#SLURM")]) || line[len("#SLURM")] == '-'):
			rest = line[len("#SLURM"):]
		default:
			if isCommandLine(line) {
				return result, nil
			}
			continue
		}

		trimmed := trimLeadingASCIIWhitespace(rest)
		if isHetJobDirective(trimmed) {
			return ParsedScript{}, ErrHetJob
		}

		tokens, err := tokenizeLine(lineno, trimmed)
		if err != nil {
			return ParsedScript{}, err
		}
		if len(tokens) == 0 {
			continue
		}
		if err := classifyDirectiveTokens(lineno, tokens, &result); err != nil {
			return ParsedScript{}, err
		}
	}

	return result, nil
}

// isCommandLine reports whether line's first non-whitespace character is not '#',
// matching Slurm 25.11 `src/sbatch/opt.c:603-615`.
func isCommandLine(line string) bool {
	for i := 0; i < len(line); i++ {
		if isASCIIWhitespace(line[i]) {
			continue
		}
		return line[i] != '#'
	}
	return false
}

func isHetJobDirective(s string) bool {
	if len(s) >= 6 && strings.EqualFold(s[:6], "hetjob") && (len(s) == 6 || isASCIIWhitespace(s[6])) {
		return true
	}
	if len(s) >= 7 && strings.EqualFold(s[:7], "packjob") && (len(s) == 7 || isASCIIWhitespace(s[7])) {
		return true
	}
	return false
}

// Tokenize splits an option string into arguments using Slurm 25.11's
// `get_argument` rules (`src/sbatch/opt.c:473-556`):
//   - Leading ASCII whitespace is skipped.
//   - Single (`'`) and double (`"`) quotes group characters (including
//     whitespace and `#`) until the matching closing quote. Unmatched quotes
//     return an error (`opt.c:548-551`).
//   - Backslash before a space (`\ `) appends a literal space (`opt.c:517-523`).
//     Backslash before any other character sets `escape_flag` so the next
//     character (such as `\"`, `\'`, `\#`, or `\\`) is included literally
//     without ending a quote or starting a comment (`opt.c:515-527`).
//   - An unquoted, unescaped `#` starts an inline comment for the remainder of
//     the line (`opt.c:538-541`).
//   - An unquoted `:` at the start of a token is rejected (`opt.c:504-507`),
//     and `hetjob`/`packjob` tokens are rejected with ErrHetJob.
func Tokenize(input string) ([]string, error) {
	var all []string
	lines := strings.Split(input, "\n")
	for idx, rawLine := range lines {
		line := strings.TrimSuffix(rawLine, "\r")
		tokens, err := tokenizeLine(idx+1, line)
		if err != nil {
			return nil, err
		}
		all = append(all, tokens...)
	}
	return all, nil
}

func tokenizeLine(lineno int, line string) ([]string, error) {
	var tokens []string
	ptr := 0
	n := len(line)

	for ptr < n {
		for ptr < n && isASCIIWhitespace(line[ptr]) {
			ptr++
		}
		if ptr >= n {
			break
		}
		if isHetJobDirective(line[ptr:]) {
			return nil, ErrHetJob
		}
		if line[ptr] == ':' {
			return nil, fmt.Errorf("line %d: unexpected `:` in %q", lineno, line)
		}
		if line[ptr] == '#' {
			break
		}

		var arg strings.Builder
		started := false
		quoted := false
		var qChar byte
		escapeFlag := false

		for ptr < n && (quoted || !isASCIIWhitespace(line[ptr])) && line[ptr] != '\n' {
			ch := line[ptr]
			if escapeFlag {
				escapeFlag = false
				started = true
				arg.WriteByte(ch)
				ptr++
				continue
			}
			if ch == '\\' {
				ptr++
				if ptr < n && line[ptr] == ' ' {
					started = true
					arg.WriteByte(' ')
					ptr++
					escapeFlag = false
				} else {
					escapeFlag = true
				}
				continue
			}
			if quoted {
				if ch == qChar {
					quoted = false
					started = true
					ptr++
					continue
				}
				started = true
				arg.WriteByte(ch)
				ptr++
				continue
			}
			if ch == '"' || ch == '\'' {
				quoted = true
				started = true
				qChar = ch
				ptr++
				continue
			}
			if ch == '#' {
				// Found an unescaped, unquoted '#': rest of line is a comment (opt.c:538-541).
				ptr = n
				break
			}
			started = true
			arg.WriteByte(ch)
			ptr++
		}

		if quoted {
			return nil, fmt.Errorf("line %d: unmatched `%c` in %q", lineno, qChar, line)
		}
		if started {
			tokens = append(tokens, arg.String())
		}
	}

	return tokens, nil
}

func classifyDirectiveTokens(lineno int, tokens []string, out *ParsedScript) error {
	var passthrough []string
	i := 0
	for i < len(tokens) {
		tok := tokens[i]
		if tok == "--" {
			passthrough = append(passthrough, tokens[i:]...)
			break
		}

		// Long option: --name=value or --name [value]
		if strings.HasPrefix(tok, "--") {
			namePart := tok[2:]
			optName, inlineVal, hasEq := strings.Cut(namePart, "=")
			if id, canonicalFlag, matched := matchLongOption(optName); matched {
				if hasEq {
					out.Fields[id] = ScriptValue{Value: inlineVal, Flag: canonicalFlag, Line: lineno}
					i++
					continue
				}
				if i+1 >= len(tokens) {
					return fmt.Errorf("line %d: option %q requires an argument", lineno, tok)
				}
				out.Fields[id] = ScriptValue{Value: tokens[i+1], Flag: canonicalFlag, Line: lineno}
				i += 2
				continue
			}

			// Unknown/passthrough long option.
			if hasEq || !longOptionTakesSeparateArg(optName) || i+1 >= len(tokens) || strings.HasPrefix(tokens[i+1], "-") {
				passthrough = append(passthrough, tok)
				i++
			} else {
				passthrough = append(passthrough, tok, tokens[i+1])
				i += 2
			}
			continue
		}

		// Short option(s): -Xval, -X=val, or -X val (or bundled boolean flags like -vH).
		if strings.HasPrefix(tok, "-") && len(tok) > 1 {
			short := tok[1]
			rest := tok[2:]
			if id, canonicalFlag, matched := matchShortOption(short); matched {
				if rest != "" {
					// Note: getopt_long takes rest directly as optarg (e.g. -Jfoo -> "foo").
					out.Fields[id] = ScriptValue{Value: rest, Flag: canonicalFlag, Line: lineno}
					i++
					continue
				}
				if i+1 >= len(tokens) {
					return fmt.Errorf("line %d: option %q requires an argument", lineno, tok)
				}
				out.Fields[id] = ScriptValue{Value: tokens[i+1], Flag: canonicalFlag, Line: lineno}
				i += 2
				continue
			}

			// Unknown/passthrough short option.
			if rest == "" && shortOptionTakesSeparateArg(short) && i+1 < len(tokens) {
				passthrough = append(passthrough, tok, tokens[i+1])
				i += 2
			} else {
				passthrough = append(passthrough, tok)
				i++
			}
			continue
		}

		passthrough = append(passthrough, tok)
		i++
	}

	if len(passthrough) > 0 {
		out.Passthrough = append(out.Passthrough, PassthroughDirective{
			Line:   lineno,
			Tokens: passthrough,
		})
	}
	return nil
}

func matchLongOption(name string) (FieldID, string, bool) {
	switch name {
	case "job-name":
		return FieldJobName, "--job-name", true
	case "account":
		return FieldAccount, "--account", true
	case "partition":
		return FieldPartition, "--partition", true
	case "qos":
		return FieldQOS, "--qos", true
	case "time":
		return FieldTime, "--time", true
	case "nodes":
		return FieldNodes, "--nodes", true
	case "ntasks":
		return FieldNTasks, "--ntasks", true
	case "cpus-per-task":
		return FieldCPUsPerTask, "--cpus-per-task", true
	case "mem":
		return FieldMem, "--mem", true
	case "gres":
		return FieldGresGPUs, "--gres", true
	case "gpus":
		return FieldGresGPUs, "--gpus", true
	case "array":
		return FieldArray, "--array", true
	case "output":
		return FieldOutput, "--output", true
	case "error":
		return FieldError, "--error", true
	case "chdir":
		return FieldChdir, "--chdir", true
	default:
		return "", "", false
	}
}

func matchShortOption(short byte) (FieldID, string, bool) {
	switch short {
	case 'J':
		return FieldJobName, "--job-name", true
	case 'A':
		return FieldAccount, "--account", true
	case 'p':
		return FieldPartition, "--partition", true
	case 'q':
		return FieldQOS, "--qos", true
	case 't':
		return FieldTime, "--time", true
	case 'N':
		return FieldNodes, "--nodes", true
	case 'n':
		return FieldNTasks, "--ntasks", true
	case 'c':
		return FieldCPUsPerTask, "--cpus-per-task", true
	case 'G':
		return FieldGresGPUs, "--gpus", true
	case 'a':
		return FieldArray, "--array", true
	case 'o':
		return FieldOutput, "--output", true
	case 'e':
		return FieldError, "--error", true
	case 'D':
		return FieldChdir, "--chdir", true
	default:
		return "", "", false
	}
}

// shortOptionTakesSeparateArg reports whether a non-fixed sbatch short option
// takes a required argument in Slurm 25.11 `src/common/slurm_opt.c` (`common_options[]`):
//
//	-b (--begin), -B (--extra-node-info), -C (--constraint), -d (--dependency),
//	-F (--nodefile), -i (--input), -L (--licenses), -m (--distribution),
//	-M (--clusters), -S (--core-spec), -w (--nodelist), -x (--exclude)
func shortOptionTakesSeparateArg(short byte) bool {
	switch short {
	case 'b', 'B', 'C', 'd', 'F', 'i', 'L', 'm', 'M', 'S', 'w', 'x':
		return true
	default:
		return false
	}
}

// longOptionTakesSeparateArg reports whether a non-fixed sbatch long option has
// `required_argument` (rather than `no_argument` or `optional_argument`) in
// Slurm 25.11 `src/common/slurm_opt.c:4213-4383`.
func longOptionTakesSeparateArg(name string) bool {
	switch name {
	// Boolean / optional-argument sbatch flags in slurm_opt.c:
	case "consolidate-segments", "contiguous", "exclusive", "external",
		"get-user-env", "help", "hold", "ignore-pbs", "nice", "no-kill",
		"no-requeue", "oom-kill-step", "overcommit", "oversubscribe",
		"parsable", "propagate", "quiet", "reboot", "requeue", "resv-ports",
		"spread-job", "spread-segments", "stepmgr", "test-only",
		"use-min-nodes", "usage", "verbose", "version", "wait":
		return false
	default:
		return true
	}
}

func isASCIIWhitespace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

func trimLeadingASCIIWhitespace(s string) string {
	i := 0
	for i < len(s) && isASCIIWhitespace(s[i]) {
		i++
	}
	return s[i:]
}
