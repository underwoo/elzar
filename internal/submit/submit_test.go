package submit

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseScriptRules(t *testing.T) {
	t.Run("directives up to first non-comment non-blank line", func(t *testing.T) {
		script := strings.Join([]string{
			"#!/bin/bash",
			"",
			"   # ordinary comment before directives",
			"#SBATCH -J first-name",
			"#SBATCH --account=climate",
			"#SBATCH --partition batch",
			"  ",
			"#SLURM -q high",
			"echo 'first command line'",
			"#SBATCH -J ignored-after-command",
			"#SBATCH --account=ignored",
		}, "\n")

		parsed, err := ParseScript(script)
		if err != nil {
			t.Fatal(err)
		}
		want := map[FieldID]ScriptValue{
			FieldJobName:   {Value: "first-name", Flag: "--job-name", Line: 4},
			FieldAccount:   {Value: "climate", Flag: "--account", Line: 5},
			FieldPartition: {Value: "batch", Flag: "--partition", Line: 6},
			FieldQOS:       {Value: "high", Flag: "--qos", Line: 8},
		}
		if !reflect.DeepEqual(parsed.Fields, want) {
			t.Fatalf("Fields = %+v, want %+v", parsed.Fields, want)
		}
	})

	t.Run("all fixed fields short and long forms and later directive wins", func(t *testing.T) {
		script := strings.Join([]string{
			"#!/bin/sh",
			"#SBATCH -J early-name",
			"#SBATCH --job-name=final-name",
			"#SBATCH -A acct1",
			"#SBATCH -p part1",
			"#SBATCH -q qos1",
			"#SBATCH -t 01:30:00",
			"#SBATCH -N 2-4",
			"#SBATCH -n 16",
			"#SBATCH -c 8",
			"#SBATCH --mem=32G",
			"#SBATCH --gres=gpu:a100:2",
			"#SBATCH -a 1-10%2",
			"#SBATCH -o logs/%x-%j.out",
			"#SBATCH -e logs/%x-%j.err",
			"#SBATCH -D /scratch/work",
		}, "\n")

		parsed, err := ParseScript(script)
		if err != nil {
			t.Fatal(err)
		}
		checks := []struct {
			id   FieldID
			val  string
			flag string
			line int
		}{
			{FieldJobName, "final-name", "--job-name", 3},
			{FieldAccount, "acct1", "--account", 4},
			{FieldPartition, "part1", "--partition", 5},
			{FieldQOS, "qos1", "--qos", 6},
			{FieldTime, "01:30:00", "--time", 7},
			{FieldNodes, "2-4", "--nodes", 8},
			{FieldNTasks, "16", "--ntasks", 9},
			{FieldCPUsPerTask, "8", "--cpus-per-task", 10},
			{FieldMem, "32G", "--mem", 11},
			{FieldGresGPUs, "gpu:a100:2", "--gres", 12},
			{FieldArray, "1-10%2", "--array", 13},
			{FieldOutput, "logs/%x-%j.out", "--output", 14},
			{FieldError, "logs/%x-%j.err", "--error", 15},
			{FieldChdir, "/scratch/work", "--chdir", 16},
		}
		for _, c := range checks {
			got, ok := parsed.Fields[c.id]
			if !ok || got.Value != c.val || got.Flag != c.flag || got.Line != c.line {
				t.Fatalf("field %s = %+v, want {Value:%q Flag:%q Line:%d}", c.id, got, c.val, c.flag, c.line)
			}
		}
	})

	t.Run("attached short option value and gpus forms", func(t *testing.T) {
		script := strings.Join([]string{
			"#!/bin/sh",
			"#SBATCH -Jattached-name -N4 -c2 -G 4",
		}, "\n")
		parsed, err := ParseScript(script)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Fields[FieldJobName].Value != "attached-name" ||
			parsed.Fields[FieldNodes].Value != "4" ||
			parsed.Fields[FieldCPUsPerTask].Value != "2" ||
			parsed.Fields[FieldGresGPUs].Value != "4" ||
			parsed.Fields[FieldGresGPUs].Flag != "--gpus" {
			t.Fatalf("parsed.Fields = %+v", parsed.Fields)
		}
	})

	t.Run("quoting escaped spaces inline comments and hash in quotes", func(t *testing.T) {
		script := strings.Join([]string{
			"#!/bin/sh",
			`#SBATCH --job-name="my #1 job" # trailing comment is ignored`,
			`#SBATCH -o '/tmp/out file #2.log'`,
			`#SBATCH -D /scratch/dir\ with\ spaces`,
			`#SBATCH -e /tmp/escaped\#hash.err`,
		}, "\n")
		parsed, err := ParseScript(script)
		if err != nil {
			t.Fatal(err)
		}
		if got := parsed.Fields[FieldJobName].Value; got != "my #1 job" {
			t.Fatalf("job-name = %q, want %q", got, "my #1 job")
		}
		if got := parsed.Fields[FieldOutput].Value; got != "/tmp/out file #2.log" {
			t.Fatalf("output = %q", got)
		}
		if got := parsed.Fields[FieldChdir].Value; got != "/scratch/dir with spaces" {
			t.Fatalf("chdir = %q", got)
		}
		if got := parsed.Fields[FieldError].Value; got != "/tmp/escaped#hash.err" {
			t.Fatalf("error = %q", got)
		}
	})

	t.Run("passthrough directives outside fixed set", func(t *testing.T) {
		script := strings.Join([]string{
			"#!/bin/sh",
			"#SBATCH -J my-job --exclusive --constraint=rome",
			"#SBATCH -C milan --mail-type END,FAIL --requeue",
		}, "\n")
		parsed, err := ParseScript(script)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Fields[FieldJobName].Value != "my-job" {
			t.Fatalf("job-name = %+v", parsed.Fields[FieldJobName])
		}
		wantPass := []PassthroughDirective{
			{Line: 2, Tokens: []string{"--exclusive", "--constraint=rome"}},
			{Line: 3, Tokens: []string{"-C", "milan", "--mail-type", "END,FAIL", "--requeue"}},
		}
		if !reflect.DeepEqual(parsed.Passthrough, wantPass) {
			t.Fatalf("Passthrough = %+v, want %+v", parsed.Passthrough, wantPass)
		}
	})

	t.Run("indented #SBATCH is ignored as ordinary comment per opt.c:591", func(t *testing.T) {
		script := "#!/bin/sh\n  #SBATCH -J indented\n#SBATCH -J column-zero\n"
		parsed, err := ParseScript(script)
		if err != nil {
			t.Fatal(err)
		}
		if got := parsed.Fields[FieldJobName]; got.Value != "column-zero" || got.Line != 3 {
			t.Fatalf("job-name = %+v", got)
		}
	})

	t.Run("hetjob and packjob separators refused", func(t *testing.T) {
		for _, sep := range []string{
			"#SBATCH hetjob",
			"#SBATCH HETJOB  ",
			"#SBATCH packjob",
			"#SBATCH -N 1\n#SBATCH hetjob\n#SBATCH -N 2",
		} {
			_, err := ParseScript("#!/bin/sh\n" + sep + "\nsrun hostname\n")
			if !errors.Is(err, ErrHetJob) || err.Error() != "heterogeneous job scripts aren't supported yet" {
				t.Fatalf("sep %q: err = %v, want %v", sep, err, ErrHetJob)
			}
		}
	})

	t.Run("unmatched quote and unexpected colon errors", func(t *testing.T) {
		if _, err := ParseScript("#!/bin/sh\n#SBATCH -J \"unclosed\n"); err == nil || !strings.Contains(err.Error(), "unmatched") {
			t.Fatalf("expected unmatched quote error, got %v", err)
		}
		if _, err := ParseScript("#!/bin/sh\n#SBATCH : -N 2\n"); err == nil || !strings.Contains(err.Error(), "unexpected `:`") {
			t.Fatalf("expected unexpected `:` error, got %v", err)
		}
		if _, err := ParseScript("#!/bin/sh\n#SBATCH --job-name\n"); err == nil || !strings.Contains(err.Error(), "requires an argument") {
			t.Fatalf("expected missing argument error, got %v", err)
		}
	})
}

func TestPrecedenceEveryFieldAndSourceCombination(t *testing.T) {
	script := strings.Join([]string{
		"#!/bin/sh",
		"#SBATCH --job-name=script-job",
		"#SBATCH --account=script-acct",
		"#SBATCH --partition=script-part",
		"#SBATCH --qos=script-qos",
		"#SBATCH --time=00:30:00",
		"#SBATCH --nodes=2",
		"#SBATCH --ntasks=8",
		"#SBATCH --cpus-per-task=4",
		"#SBATCH --mem=16G",
		"#SBATCH --gres=gpu:1",
		"#SBATCH --array=0-3",
		"#SBATCH --output=script.out",
		"#SBATCH --error=script.err",
		"#SBATCH --chdir=/script/dir",
	}, "\n")

	parsed, err := ParseScript(script)
	if err != nil {
		t.Fatal(err)
	}
	emptyParsed := ParsedScript{Fields: map[FieldID]ScriptValue{}}

	// Map of field -> primary SBATCH_* input variable from Slurm 25.11 man sbatch.
	envVarForField := map[FieldID]string{
		FieldJobName:   "SBATCH_JOB_NAME",
		FieldAccount:   "SBATCH_ACCOUNT",
		FieldPartition: "SBATCH_PARTITION",
		FieldQOS:       "SBATCH_QOS",
		FieldTime:      "SBATCH_TIMELIMIT",
		FieldMem:       "SBATCH_MEM_PER_NODE",
		FieldGresGPUs:  "SBATCH_GPUS",
		FieldArray:     "SBATCH_ARRAY_INX",
		FieldOutput:    "SBATCH_OUTPUT",
		FieldError:     "SBATCH_ERROR",
	}

	// Non-input SLURM_* variables that must NOT be treated as sbatch env sources.
	nonInputEnv := map[string]string{
		"SLURM_NNODES":         "99",
		"SLURM_JOB_NUM_NODES":  "99",
		"SLURM_NTASKS":         "99",
		"SLURM_NPROCS":         "99",
		"SLURM_CPUS_PER_TASK":  "99",
		"SLURM_WORKING_DIR":    "/bad/dir",
		"SBATCH_CHDIR":         "/bad/dir",
		"SBATCH_NODES":         "99",
		"SBATCH_NTASKS":        "99",
		"SBATCH_CPUS_PER_TASK": "99",
	}

	for _, spec := range FixedFields() {
		t.Run(string(spec.ID), func(t *testing.T) {
			// 1. Default source (neither script nor env nor form).
			plan, err := Resolve("/work/job.sh", emptyParsed, func(k string) (string, bool) {
				v, ok := nonInputEnv[k]
				return v, ok
			}, Edits{})
			if err != nil {
				t.Fatal(err)
			}
			f, _ := plan.Field(spec.ID)
			if f.Value != "" || f.Source.Kind != SourceDefault || f.Source.Label() != "default" || f.Edited {
				t.Fatalf("default state for %s = %+v (label=%q)", spec.ID, f, f.Source.Label())
			}

			// 2. Script source (#SBATCH).
			plan, err = Resolve("/work/job.sh", parsed, func(k string) (string, bool) {
				v, ok := nonInputEnv[k]
				return v, ok
			}, Edits{})
			if err != nil {
				t.Fatal(err)
			}
			f, _ = plan.Field(spec.ID)
			wantScript := parsed.Fields[spec.ID]
			if f.Value != wantScript.Value || f.Source.Kind != SourceScript || f.Source.Line != wantScript.Line {
				t.Fatalf("script state for %s = %+v, want %+v", spec.ID, f, wantScript)
			}

			// 3. Env source (overrides script when field has an SBATCH_* input variable).
			envVar, hasEnvVar := envVarForField[spec.ID]
			envMap := map[string]string{}
			if hasEnvVar {
				envMap[envVar] = "env-val-" + string(spec.ID)
			}
			plan, err = Resolve("/work/job.sh", parsed, func(k string) (string, bool) {
				v, ok := envMap[k]
				return v, ok
			}, Edits{})
			if err != nil {
				t.Fatal(err)
			}
			f, _ = plan.Field(spec.ID)
			if hasEnvVar {
				if f.Value != "env-val-"+string(spec.ID) || f.Source.Kind != SourceEnv || f.Source.Label() != "env "+envVar {
					t.Fatalf("env state for %s = %+v (label=%q)", spec.ID, f, f.Source.Label())
				}
			} else {
				if f.Source.Kind != SourceScript {
					t.Fatalf("field %s without input env var unexpectedly had source %+v", spec.ID, f.Source)
				}
			}

			// 4. Form edit source (overrides both env and script).
			edits := Edits{
				Values: map[FieldID]string{spec.ID: "form-val-" + string(spec.ID)},
			}
			plan, err = Resolve("/work/job.sh", parsed, func(k string) (string, bool) {
				v, ok := envMap[k]
				return v, ok
			}, edits)
			if err != nil {
				t.Fatal(err)
			}
			f, _ = plan.Field(spec.ID)
			if f.Value != "form-val-"+string(spec.ID) || f.Source.Kind != SourceForm || f.Source.Label() != "form" || !f.Edited {
				t.Fatalf("form state for %s = %+v", spec.ID, f)
			}
		})
	}

	// Verify SBATCH_GRES vs SBATCH_GPUS ordering in env_vars[] (opt.c:203-205):
	// SBATCH_GRES is processed at line 203, SBATCH_GPUS at line 205.
	t.Run("SBATCH_GRES and SBATCH_GPUS env precedence", func(t *testing.T) {
		plan, err := Resolve("/work/job.sh", emptyParsed, func(k string) (string, bool) {
			if k == "SBATCH_GRES" {
				return "gpu:v100:2", true
			}
			return "", false
		}, Edits{})
		if err != nil {
			t.Fatal(err)
		}
		f, _ := plan.Field(FieldGresGPUs)
		if f.Value != "gpu:v100:2" || f.Flag != "--gres" || f.Source.Label() != "env SBATCH_GRES" {
			t.Fatalf("SBATCH_GRES resolved to %+v", f)
		}

		plan, err = Resolve("/work/job.sh", emptyParsed, func(k string) (string, bool) {
			switch k {
			case "SBATCH_GRES":
				return "gpu:v100:2", true
			case "SBATCH_GPUS":
				return "h100:4", true
			}
			return "", false
		}, Edits{})
		if err != nil {
			t.Fatal(err)
		}
		f, _ = plan.Field(FieldGresGPUs)
		if f.Value != "h100:4" || f.Flag != "--gpus" || f.Source.Label() != "env SBATCH_GPUS" {
			t.Fatalf("SBATCH_GPUS resolved to %+v", f)
		}
	})
}

func TestArgvOnlyEditedFieldsAndWorkingDir(t *testing.T) {
	script := strings.Join([]string{
		"#!/bin/sh",
		"#SBATCH -J script-name",
		"#SBATCH -A script-acct",
		"#SBATCH -p batch",
		"#SBATCH -N 2",
	}, "\n")
	parsed, err := ParseScript(script)
	if err != nil {
		t.Fatal(err)
	}
	env := func(k string) (string, bool) {
		if k == "SBATCH_QOS" {
			return "high", true
		}
		return "", false
	}

	// 1. Unedited form: argv has only sbatch, --parsable, [--test-only], and absolute script path.
	plan, err := Resolve("/scratch/user/jobs/run.sh", parsed, env, Edits{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.WorkingDir != "/scratch/user/jobs" {
		t.Fatalf("WorkingDir = %q, want /scratch/user/jobs", plan.WorkingDir)
	}
	wantUnedited := []string{"sbatch", "--parsable", "/scratch/user/jobs/run.sh"}
	if got := plan.Argv(false); !reflect.DeepEqual(got, wantUnedited) {
		t.Fatalf("unedited Argv(false) = %v, want %v", got, wantUnedited)
	}
	wantDry := []string{"sbatch", "--parsable", "--test-only", "/scratch/user/jobs/run.sh"}
	if got := plan.Argv(true); !reflect.DeepEqual(got, wantDry) {
		t.Fatalf("unedited Argv(true) = %v, want %v", got, wantDry)
	}

	// 2. Edited fields + extra options + script args.
	edits := Edits{
		Values: map[FieldID]string{
			FieldJobName: "script-name", // identical to base -> not edited
			FieldAccount: "override-acct",
			FieldTime:    "02:00:00",
			FieldChdir:   "subdir",
		},
		ExtraArgs:  `--exclusive --comment="run 1"`,
		ScriptArgs: `--epochs 10 "input file.dat"`,
	}
	plan, err = Resolve("/scratch/user/jobs/run.sh", parsed, env, edits)
	if err != nil {
		t.Fatal(err)
	}
	if plan.WorkingDir != filepath.Join("/scratch/user/jobs", "subdir") {
		t.Fatalf("WorkingDir with relative chdir = %q", plan.WorkingDir)
	}
	wantEdited := []string{
		"sbatch",
		"--parsable",
		"--account=override-acct",
		"--time=02:00:00",
		"--chdir=subdir",
		"--exclusive",
		"--comment=run 1",
		"/scratch/user/jobs/run.sh",
		"--epochs",
		"10",
		"input file.dat",
	}
	if got := plan.Argv(false); !reflect.DeepEqual(got, wantEdited) {
		t.Fatalf("edited Argv(false) =\n  %v\nwant:\n  %v", got, wantEdited)
	}
}
