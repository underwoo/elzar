// Package submit implements sbatch script directive parsing, input environment
// variable precedence, form resolution, and argv generation without depending
// on the UI.
//
// All rules follow Slurm 25.11 src/sbatch/opt.c, src/common/slurm_opt.c,
// src/sbatch/sbatch.c, and doc/man/man1/sbatch.1 (man sbatch).
package submit

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// ErrHetJob is returned when a script contains a `#SBATCH hetjob` or
// `#SBATCH packjob` component separator (Slurm 25.11 src/sbatch/opt.c:624-630).
var ErrHetJob = errors.New("heterogeneous job scripts aren't supported yet")

// FieldID identifies one of the fixed sbatch option fields or free-text inputs
// exposed on the submit form.
type FieldID string

const (
	FieldJobName     FieldID = "job-name"
	FieldAccount     FieldID = "account"
	FieldPartition   FieldID = "partition"
	FieldQOS         FieldID = "qos"
	FieldTime        FieldID = "time"
	FieldNodes       FieldID = "nodes"
	FieldNTasks      FieldID = "ntasks"
	FieldCPUsPerTask FieldID = "cpus-per-task"
	FieldMem         FieldID = "mem"
	FieldGresGPUs    FieldID = "gres-gpus"
	FieldArray       FieldID = "array"
	FieldOutput      FieldID = "output"
	FieldError       FieldID = "error"
	FieldChdir       FieldID = "chdir"
	FieldClusters    FieldID = "clusters"

	FieldExtraArgs  FieldID = "extra-sbatch-options"
	FieldScriptArgs FieldID = "script-arguments"
)

// SourceKind identifies where a field's effective value came from.
type SourceKind string

const (
	SourceDefault SourceKind = "default"
	SourceScript  SourceKind = "script"
	SourceEnv     SourceKind = "env"
	SourceForm    SourceKind = "form"
)

// Source describes the origin of a field's effective value and formats its
// display label (`script:<line>`, `env SBATCH_X`, `form`, or `default`).
type Source struct {
	Kind   SourceKind
	Line   int
	EnvVar string
}

func (s Source) Label() string {
	switch s.Kind {
	case SourceScript:
		return fmt.Sprintf("script:%d", s.Line)
	case SourceEnv:
		return "env " + s.EnvVar
	case SourceForm:
		return "form"
	default:
		return "default"
	}
}

// FieldSpec defines metadata for one fixed sbatch option field.
type FieldSpec struct {
	ID        FieldID
	Label     string
	ShortFlag string
	LongFlag  string
	EnvVars   []string
}

// FixedFields lists the 15 sbatch option fields in form display order.
//
// Env variable mappings come directly from Slurm 25.11 src/sbatch/opt.c:182-248
// (`env_vars[]`) and doc/man/man1/sbatch.1 ("INPUT ENVIRONMENT VARIABLES",
// lines 2821-3159):
//   - job-name (-J, --job-name): SBATCH_JOB_NAME
//   - account (-A, --account): SBATCH_ACCOUNT
//   - partition (-p, --partition): SBATCH_PARTITION
//   - qos (-q, --qos): SBATCH_QOS
//   - time (-t, --time): SBATCH_TIMELIMIT
//   - nodes (-N, --nodes): none (SLURM_NNODES is an output variable only)
//   - ntasks (-n, --ntasks): none (SLURM_NTASKS is an sbatch output / srun input variable)
//   - cpus-per-task (-c, --cpus-per-task): none (SLURM_CPUS_PER_TASK is an output variable)
//   - mem (--mem): SBATCH_MEM_PER_NODE
//   - gres/gpus (--gres, -G, --gpus): SBATCH_GRES, SBATCH_GPUS
//   - array (-a, --array): SBATCH_ARRAY_INX
//   - output (-o, --output): SBATCH_OUTPUT
//   - error (-e, --error): SBATCH_ERROR
//   - chdir (-D, --chdir): none (SLURM_WORKING_DIR is not in sbatch env_vars[])
//   - clusters (-M, --clusters, --cluster): SBATCH_CLUSTERS, SLURM_CLUSTERS (opt.c:189-190, sbatch.1:2856-2857)
func FixedFields() []FieldSpec {
	return slices.Clone(fixedFields)
}

var fixedFields = []FieldSpec{
	{ID: FieldJobName, Label: "Job name", ShortFlag: "-J", LongFlag: "--job-name", EnvVars: []string{"SBATCH_JOB_NAME"}},
	{ID: FieldAccount, Label: "Account", ShortFlag: "-A", LongFlag: "--account", EnvVars: []string{"SBATCH_ACCOUNT"}},
	{ID: FieldPartition, Label: "Partition", ShortFlag: "-p", LongFlag: "--partition", EnvVars: []string{"SBATCH_PARTITION"}},
	{ID: FieldQOS, Label: "QOS", ShortFlag: "-q", LongFlag: "--qos", EnvVars: []string{"SBATCH_QOS"}},
	{ID: FieldTime, Label: "Time", ShortFlag: "-t", LongFlag: "--time", EnvVars: []string{"SBATCH_TIMELIMIT"}},
	{ID: FieldNodes, Label: "Nodes", ShortFlag: "-N", LongFlag: "--nodes"},
	{ID: FieldNTasks, Label: "NTasks", ShortFlag: "-n", LongFlag: "--ntasks"},
	{ID: FieldCPUsPerTask, Label: "CPUs/task", ShortFlag: "-c", LongFlag: "--cpus-per-task"},
	{ID: FieldMem, Label: "Mem", LongFlag: "--mem", EnvVars: []string{"SBATCH_MEM_PER_NODE"}},
	{ID: FieldGresGPUs, Label: "GRES/GPUs", ShortFlag: "-G", LongFlag: "--gpus", EnvVars: []string{"SBATCH_GRES", "SBATCH_GPUS"}},
	{ID: FieldArray, Label: "Array", ShortFlag: "-a", LongFlag: "--array", EnvVars: []string{"SBATCH_ARRAY_INX"}},
	{ID: FieldOutput, Label: "Output", ShortFlag: "-o", LongFlag: "--output", EnvVars: []string{"SBATCH_OUTPUT"}},
	{ID: FieldError, Label: "Error", ShortFlag: "-e", LongFlag: "--error", EnvVars: []string{"SBATCH_ERROR"}},
	{ID: FieldChdir, Label: "Chdir", ShortFlag: "-D", LongFlag: "--chdir"},
	{ID: FieldClusters, Label: "Clusters", ShortFlag: "-M", LongFlag: "--clusters", EnvVars: []string{"SBATCH_CLUSTERS", "SLURM_CLUSTERS"}},
}

// ScriptValue records a field value parsed from a `#SBATCH` directive line.
type ScriptValue struct {
	Value string
	Flag  string // canonical long flag ("--gres" or "--gpus" for FieldGresGPUs)
	Line  int
}

// PassthroughDirective records a `#SBATCH` directive outside the fixed field
// set so the UI can display it read-only under "passed through by sbatch".
type PassthroughDirective struct {
	Line   int
	Tokens []string
}

func (p PassthroughDirective) String() string {
	return strings.Join(p.Tokens, " ")
}

// ParsedScript holds the `#SBATCH` directives extracted from a batch script.
type ParsedScript struct {
	Fields      map[FieldID]ScriptValue
	Passthrough []PassthroughDirective
}

// EnvLookup looks up an environment variable (matching `os.LookupEnv`).
type EnvLookup func(string) (string, bool)

// ResolvedField holds the effective value and provenance of one field.
type ResolvedField struct {
	Spec           FieldSpec
	Value          string
	Flag           string // "--gres" or "--gpus" for FieldGresGPUs; Spec.LongFlag otherwise
	Source         Source
	Edited         bool
	ClearRequested bool
}

// Edits holds user modifications made in the submit form.
// Only fields present in Values (and differing from the base script/env value,
// or explicitly marked in Edited) are emitted on the sbatch command line.
type Edits struct {
	Values     map[FieldID]string
	Edited     map[FieldID]bool
	ExtraArgs  string
	ScriptArgs string
}

// Plan is the resolved submit state for a script, environment, and form edits.
type Plan struct {
	ScriptPath  string
	WorkingDir  string
	Fields      []ResolvedField
	byID        map[FieldID]ResolvedField
	Passthrough []PassthroughDirective
	ExtraArgs   []string
	ScriptArgs  []string
}

// Field returns the resolved state for id.
func (p Plan) Field(id FieldID) (ResolvedField, bool) {
	f, ok := p.byID[id]
	return f, ok
}

// Resolve computes the effective value and source for every fixed field using
// sbatch's precedence (form/CLI > SBATCH_* env > #SBATCH > default; Slurm 25.11
// src/sbatch/opt.c:364-410, `process_options_second_pass`), determines the
// working directory (the script's directory unless chdir is set), and tokenizes
// extra sbatch options and script arguments.
func Resolve(scriptPath string, parsed ParsedScript, env EnvLookup, edits Edits) (Plan, error) {
	absScript := scriptPath
	if absScript != "" && !filepath.IsAbs(absScript) {
		if resolved, err := filepath.Abs(absScript); err == nil {
			absScript = resolved
		}
	}

	var extraTokens []string
	if strings.TrimSpace(edits.ExtraArgs) != "" {
		tokens, err := Tokenize(edits.ExtraArgs)
		if err != nil {
			return Plan{}, fmt.Errorf("extra sbatch options: %w", err)
		}
		extraTokens = tokens
	}
	var scriptTokens []string
	if strings.TrimSpace(edits.ScriptArgs) != "" {
		tokens, err := Tokenize(edits.ScriptArgs)
		if err != nil {
			return Plan{}, fmt.Errorf("script arguments: %w", err)
		}
		scriptTokens = tokens
	}

	fields := make([]ResolvedField, 0, len(fixedFields))
	byID := make(map[FieldID]ResolvedField, len(fixedFields))

	for _, spec := range fixedFields {
		baseVal, baseFlag, baseSource := resolveBase(spec, parsed, env)
		rf := ResolvedField{
			Spec:   spec,
			Value:  baseVal,
			Flag:   baseFlag,
			Source: baseSource,
		}

		editedVal, hasVal := edits.Values[spec.ID]
		isEdited := edits.Edited[spec.ID] || (hasVal && editedVal != baseVal)
		if isEdited {
			if strings.TrimSpace(editedVal) == "" {
				// The sbatch CLI cannot unset a #SBATCH directive or SBATCH_*
				// environment variable, so an empty edit resets to the base
				// script/env/default value and source, while flagging
				// ClearRequested when a non-empty base value was present.
				rf.ClearRequested = baseVal != ""
			} else {
				rf.Value = editedVal
				rf.Source = Source{Kind: SourceForm}
				rf.Edited = true
				if spec.ID == FieldGresGPUs {
					rf.Flag = gresOrGPUsFlag(editedVal, baseFlag)
				}
			}
		}

		fields = append(fields, rf)
		byID[spec.ID] = rf
	}

	workDir := filepath.Dir(absScript)
	if chdir, ok := byID[FieldChdir]; ok && strings.TrimSpace(chdir.Value) != "" {
		if filepath.IsAbs(chdir.Value) || workDir == "" || workDir == "." {
			workDir = chdir.Value
		} else {
			workDir = filepath.Join(workDir, chdir.Value)
		}
	}

	return Plan{
		ScriptPath:  absScript,
		WorkingDir:  workDir,
		Fields:      fields,
		byID:        byID,
		Passthrough: slices.Clone(parsed.Passthrough),
		ExtraArgs:   extraTokens,
		ScriptArgs:  scriptTokens,
	}, nil
}

// BaseValue returns the unedited (env > script > default) value and source for
// a field, used when the user presses ctrl+r in the form to reset a field.
func BaseValue(id FieldID, parsed ParsedScript, env EnvLookup) (string, Source) {
	for _, spec := range fixedFields {
		if spec.ID == id {
			val, _, src := resolveBase(spec, parsed, env)
			return val, src
		}
	}
	return "", Source{Kind: SourceDefault}
}

func resolveBase(spec FieldSpec, parsed ParsedScript, env EnvLookup) (value, flag string, source Source) {
	flag = spec.LongFlag
	source = Source{Kind: SourceDefault}

	// 1. Script directive (#SBATCH).
	if sv, ok := parsed.Fields[spec.ID]; ok {
		value = sv.Value
		if sv.Flag != "" {
			flag = sv.Flag
		}
		source = Source{Kind: SourceScript, Line: sv.Line}
	}

	// 2. SBATCH_* environment variables override script directives (Slurm 25.11
	// src/sbatch/opt.c:394-406: _opt_batch_script runs before _opt_env, and
	// _opt_env iterates env_vars[] in order).
	if env != nil {
		for _, envName := range spec.EnvVars {
			if envVal, ok := env(envName); ok && envVal != "" {
				value = envVal
				source = Source{Kind: SourceEnv, EnvVar: envName}
				if spec.ID == FieldGresGPUs {
					if envName == "SBATCH_GRES" {
						flag = "--gres"
					} else {
						flag = "--gpus"
					}
				}
			}
		}
	}

	return value, flag, source
}

// gresOrGPUsFlag chooses --gres when the user enters a `gres/...` or `gpu:...`
// value onto a field that came from --gres (or starts with `gres:`), and
// defaults to --gpus otherwise unless prefixed with `--gres=` / `gres:`.
func gresOrGPUsFlag(val, baseFlag string) string {
	trimmed := strings.TrimSpace(val)
	if strings.HasPrefix(trimmed, "gres/") || strings.HasPrefix(trimmed, "gres:") {
		return "--gres"
	}
	if baseFlag == "--gres" {
		return "--gres"
	}
	return "--gpus"
}

// Argv builds the sbatch command vector:
// `sbatch --parsable [--test-only] <only edited form fields> [extra sbatch options] <abs-script> [script args]`
//
// Per the M5 design decision, unedited fields from `#SBATCH` or `SBATCH_*` are
// never re-passed on the command line because sbatch reads the script and
// environment itself.
func (p Plan) Argv(testOnly bool) []string {
	argv := []string{"sbatch", "--parsable"}
	if testOnly {
		argv = append(argv, "--test-only")
	}
	for _, f := range p.Fields {
		if !f.Edited {
			continue
		}
		if f.Value == "" {
			continue
		}
		flag := f.Flag
		if flag == "" {
			flag = f.Spec.LongFlag
		}
		val := f.Value
		if f.Spec.ID == FieldGresGPUs && flag == "--gres" {
			val = strings.TrimPrefix(val, "gres:")
		}
		argv = append(argv, flag+"="+val)
	}
	argv = append(argv, p.ExtraArgs...)
	if p.ScriptPath != "" {
		argv = append(argv, p.ScriptPath)
	}
	argv = append(argv, p.ScriptArgs...)
	return argv
}
