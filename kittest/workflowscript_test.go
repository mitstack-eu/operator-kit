package kittest_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// A workflow step is a shell script nothing else type-checks, so these tests prove the helper reads and runs it.

const twoJobWorkflow = `name: ci

on:
  push:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Build
        run: echo test-build
  release:
    name: Cut the release
    if: github.ref == 'refs/heads/main'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      # The tag step reads the version the build step wrote.
      - name: Build
        if: steps.check.outputs.changed != '0'
        env:
          PLAIN: plain value
          SINGLE: 'it''s quoted'
          DOUBLE: "line\tone"
          FROM_EXPR: ${{ github.sha }}
          EMPTY:
        run: |
          echo "building $PLAIN"

          if [ -n "$SINGLE" ]; then
            echo indented
          fi
      - name: Tag
        run: echo tag
`

func TestReadWorkflowScript_ReturnsRunAndEnvByJobAndStepName(t *testing.T) {
	want := kittest.WorkflowScript{
		Run: "echo \"building $PLAIN\"\n\nif [ -n \"$SINGLE\" ]; then\n  echo indented\nfi\n",
		Env: map[string]string{
			"PLAIN":     "plain value",
			"SINGLE":    "it's quoted",
			"DOUBLE":    "line\tone",
			"FROM_EXPR": "${{ github.sha }}",
			"EMPTY":     "",
		},
	}

	got, err := kittest.ReadWorkflowScript([]byte(twoJobWorkflow), "release", "Build")

	if err != nil {
		t.Fatalf("ReadWorkflowScript(release, Build) error = %v; want nil", err)
	}
	if got.Run != want.Run {
		t.Errorf("ReadWorkflowScript(release, Build).Run = %q; want %q", got.Run, want.Run)
	}
	if !reflect.DeepEqual(got.Env, want.Env) {
		t.Errorf("ReadWorkflowScript(release, Build).Env = %v; want %v", got.Env, want.Env)
	}
}

func TestReadWorkflowScript_SelectsTheStepInsideTheNamedJob(t *testing.T) {
	got, err := kittest.ReadWorkflowScript([]byte(twoJobWorkflow), "test", "Build")

	if err != nil {
		t.Fatalf("ReadWorkflowScript(test, Build) error = %v; want nil", err)
	}
	if got.Run != "echo test-build" {
		t.Errorf("ReadWorkflowScript(test, Build).Run = %q; want %q", got.Run, "echo test-build")
	}
	if len(got.Env) != 0 {
		t.Errorf("ReadWorkflowScript(test, Build).Env = %v; want empty", got.Env)
	}
}

func TestReadWorkflowScript_ReadsEachSupportedShape(t *testing.T) {
	cases := []struct {
		name     string
		workflow string
		wantRun  string
	}{
		{
			name: "a strip-chomped literal drops the final newline",
			workflow: `jobs:
  j:
    steps:
      - name: s
        run: |-
          echo one
          echo two
`,
			wantRun: "echo one\necho two",
		},
		{
			name: "a keep-chomped literal keeps its trailing blank lines",
			workflow: `jobs:
  j:
    steps:
      - name: s
        run: |+
          echo one

`,
			wantRun: "echo one\n\n",
		},
		{
			name: "a double-quoted run is unescaped",
			workflow: `jobs:
  j:
    steps:
      - name: s
        run: "printf '%s\\n' \"quoted\""
`,
			wantRun: `printf '%s\n' "quoted"`,
		},
		{
			name: "a sequence written at its key's indent still holds the steps",
			workflow: `jobs:
  j:
    steps:
    - name: other
      run: echo other
    - name: s
      run: echo compact
`,
			wantRun: "echo compact",
		},
		{
			name: "the name key need not come first in the step",
			workflow: `jobs:
  j:
    steps:
      - run: echo late-name
        name: s
`,
			wantRun: "echo late-name",
		},
		{
			name: "a dash alone on its line opens the step on the next",
			workflow: `jobs:
  j:
    steps:
      -
        name: s
        run: echo bare-dash
`,
			wantRun: "echo bare-dash",
		},
		{
			name: "a comment after the literal header and a less indented comment after the body are not run",
			workflow: `jobs:
  j:
    steps:
      - name: s
        run: | # the body
          echo body
      # between steps
      - name: t
        run: echo t
`,
			wantRun: "echo body\n",
		},
		{
			name: "a trailing comment ends a plain run",
			workflow: `jobs:
  j:
    steps:
      - name: s
        run: echo plain # not part of the command
`,
			wantRun: "echo plain",
		},
		{
			name: "a quoted step name matches its unquoted text",
			workflow: `jobs:
  j:
    steps:
      - name: "s"
        run: echo quoted-name
`,
			wantRun: "echo quoted-name",
		},
		{
			name: "a shell named bash is the shell the runner uses",
			workflow: `jobs:
  j:
    steps:
      - name: s
        shell: bash
        run: echo bash
`,
			wantRun: "echo bash",
		},
		{
			name:     "a tab after the literal's indent is part of the body",
			workflow: "jobs:\n  j:\n    steps:\n      - name: s\n        run: |\n          cat <<-EOF\n          \tindented by tab\n          \tEOF\n",
			wantRun:  "cat <<-EOF\n\tindented by tab\n\tEOF\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := kittest.ReadWorkflowScript([]byte(tc.workflow), "j", "s")

			if err != nil {
				t.Fatalf("ReadWorkflowScript(j, s) error = %v; want nil", err)
			}
			if got.Run != tc.wantRun {
				t.Errorf("ReadWorkflowScript(j, s).Run = %q; want %q", got.Run, tc.wantRun)
			}
		})
	}
}

func TestReadWorkflowScript_RefusesWhatItCannotReadRatherThanGuess(t *testing.T) {
	cases := []struct {
		name      string
		workflow  string
		job, step string
		wantInErr string
	}{
		{
			name:      "a job the workflow does not have is named with the jobs it has",
			workflow:  twoJobWorkflow,
			job:       "deploy",
			step:      "Build",
			wantInErr: `job "deploy" not found; jobs: test, release`,
		},
		{
			name:      "a step the job does not have is named with the steps it has",
			workflow:  twoJobWorkflow,
			job:       "release",
			step:      "Publish",
			wantInErr: `step "Publish" not found in job "release"; steps: Build, Tag`,
		},
		{
			name:      "a workflow with no jobs key",
			workflow:  "name: ci\non: push\n",
			job:       "j",
			step:      "s",
			wantInErr: "no top-level jobs",
		},
		{
			name: "two steps carrying the step name",
			workflow: `jobs:
  j:
    steps:
      - name: s
        run: echo one
      - name: s
        run: echo two
`,
			job:       "j",
			step:      "s",
			wantInErr: `2 steps named "s"`,
		},
		{
			name: "a step that runs an action rather than a shell body",
			workflow: `jobs:
  j:
    steps:
      - name: s
        uses: actions/checkout@v4
`,
			job:       "j",
			step:      "s",
			wantInErr: "has no run",
		},
		{
			name: "a folded run",
			workflow: `jobs:
  j:
    steps:
      - name: s
        run: >
          echo folded
`,
			job:       "j",
			step:      "s",
			wantInErr: "folded",
		},
		{
			name: "a literal with an explicit indentation indicator",
			workflow: `jobs:
  j:
    steps:
      - name: s
        run: |2
            echo indicated
`,
			job:       "j",
			step:      "s",
			wantInErr: "indentation indicator",
		},
		{
			name: "a plain run continued over several lines",
			workflow: `jobs:
  j:
    steps:
      - name: s
        run: echo one
          echo two
`,
			job:       "j",
			step:      "s",
			wantInErr: "multi-line",
		},
		{
			name: "an env written as a flow mapping",
			workflow: `jobs:
  j:
    steps:
      - name: s
        env: {A: b}
        run: echo flow
`,
			job:       "j",
			step:      "s",
			wantInErr: "flow",
		},
		{
			name: "a run given through an alias",
			workflow: `jobs:
  j:
    steps:
      - name: s
        run: *shared
`,
			job:       "j",
			step:      "s",
			wantInErr: "alias",
		},
		{
			name:      "a key indented by a tab",
			workflow:  "jobs:\n  j:\n    steps:\n      - name: s\n\trun: echo tab\n",
			job:       "j",
			step:      "s",
			wantInErr: "tab",
		},
		{
			name: "a literal body line less indented than the body",
			workflow: `jobs:
  j:
    steps:
      - name: s
        run: |
            echo deep
          echo shallow
`,
			job:       "j",
			step:      "s",
			wantInErr: "less indented",
		},
		{
			name: "an env value written as a YAML null",
			workflow: `jobs:
  j:
    steps:
      - name: s
        env:
          A: ~
        run: echo null
`,
			job:       "j",
			step:      "s",
			wantInErr: "null",
		},
		{
			name: "a run written as a YAML null",
			workflow: `jobs:
  j:
    steps:
      - name: s
        run: null
`,
			job:       "j",
			step:      "s",
			wantInErr: "null",
		},
		{
			name: "a key given twice in one mapping",
			workflow: `jobs:
  j:
    steps:
      - name: s
        run: echo first
        run: echo second
`,
			job:       "j",
			step:      "s",
			wantInErr: "given twice",
		},
		{
			name: "a shell other than bash",
			workflow: `jobs:
  j:
    steps:
      - name: s
        shell: python
        run: print("hi")
`,
			job:       "j",
			step:      "s",
			wantInErr: `shell "python"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := kittest.ReadWorkflowScript([]byte(tc.workflow), tc.job, tc.step)

			if err == nil {
				t.Fatalf("ReadWorkflowScript(%s, %s) = %+v, nil; want an error containing %q", tc.job, tc.step, got, tc.wantInErr)
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("ReadWorkflowScript(%s, %s) error = %q; want it to contain %q", tc.job, tc.step, err, tc.wantInErr)
			}
		})
	}
}

func TestRunWorkflowScript_FailsASyntaxErrorThatASubstringAssertionPasses(t *testing.T) {
	workflow := `jobs:
  deploy:
    steps:
      - name: Deploy
        run: |
          if [ -n "$TARGET" ]; then
            echo "deploying to $TARGET"
`
	step, err := kittest.ReadWorkflowScript([]byte(workflow), "deploy", "Deploy")
	if err != nil {
		t.Fatalf("ReadWorkflowScript(deploy, Deploy) error = %v; want nil", err)
	}
	if !strings.Contains(workflow, `echo "deploying to $TARGET"`) {
		t.Fatalf("the substring assertion this test contrasts with must pass on the broken workflow")
	}

	res := kittest.RunWorkflowScript(t, step, kittest.ScriptRun{Env: map[string]string{"TARGET": "prod"}})

	if res.ExitCode == 0 {
		t.Errorf("RunWorkflowScript(unclosed if).ExitCode = 0; want non-zero, stdout %q stderr %q", res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "syntax error") {
		t.Errorf("RunWorkflowScript(unclosed if).Stderr = %q; want bash's syntax error", res.Stderr)
	}
}

func TestRunWorkflowScript_RunsTheBodyAgainstTheFakesOnPath(t *testing.T) {
	step := kittest.WorkflowScript{Run: "gh pr view 7 | tr a-z A-Z\n"}

	res := kittest.RunWorkflowScript(t, step, kittest.ScriptRun{
		Fakes: map[string]string{"gh": `echo "fake gh $*"`},
		Tools: []string{"tr"},
	})

	if res.ExitCode != 0 {
		t.Fatalf("RunWorkflowScript(gh | tr).ExitCode = %d; want 0, stderr %q", res.ExitCode, res.Stderr)
	}
	if res.Stdout != "FAKE GH PR VIEW 7\n" {
		t.Errorf("RunWorkflowScript(gh | tr).Stdout = %q; want %q", res.Stdout, "FAKE GH PR VIEW 7\n")
	}
}

func TestRunWorkflowScript_FindsNoCommandItWasNotGiven(t *testing.T) {
	step := kittest.WorkflowScript{Run: "curl https://example.invalid\n"}

	res := kittest.RunWorkflowScript(t, step, kittest.ScriptRun{})

	if res.ExitCode != 127 {
		t.Errorf("RunWorkflowScript(unfaked curl).ExitCode = %d; want 127, stderr %q", res.ExitCode, res.Stderr)
	}
}

func TestRunWorkflowScript_FailsOnAFailedPipeAnUnsetVariableAndAFailedCommand(t *testing.T) {
	cases := []struct {
		name string
		run  string
	}{
		{name: "a failing command early in a pipeline", run: "false | cat\necho after\n"},
		{name: "an unset variable", run: "echo \"$NEVER_SET\"\necho after\n"},
		{name: "a failing command", run: "false\necho after\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			step := kittest.WorkflowScript{Run: tc.run}

			res := kittest.RunWorkflowScript(t, step, kittest.ScriptRun{Tools: []string{"cat"}})

			if res.ExitCode == 0 || strings.Contains(res.Stdout, "after") {
				t.Errorf("RunWorkflowScript(%q) = exit %d, stdout %q; want non-zero before echo after", tc.run, res.ExitCode, res.Stdout)
			}
		})
	}
}

func TestRunWorkflowScript_ReturnsTheExitCodeAndBothStreams(t *testing.T) {
	step := kittest.WorkflowScript{Run: "echo out\necho err >&2\nexit 7\n"}

	res := kittest.RunWorkflowScript(t, step, kittest.ScriptRun{})

	want := kittest.ScriptResult{ExitCode: 7, Stdout: "out\n", Stderr: "err\n"}
	if res != want {
		t.Errorf("RunWorkflowScript(exit 7) = %+v; want %+v", res, want)
	}
}

func TestRunWorkflowScript_GivesTheStepItsEnvTheTestsOverridesAndNothingAmbient(t *testing.T) {
	t.Setenv("KITTEST_AMBIENT", "leaked")
	step := kittest.WorkflowScript{
		Run: `echo "$FROM_STEP $OVERRIDDEN ${KITTEST_AMBIENT:-absent}"` + "\n",
		Env: map[string]string{"FROM_STEP": "step", "OVERRIDDEN": "step"},
	}

	res := kittest.RunWorkflowScript(t, step, kittest.ScriptRun{Env: map[string]string{"OVERRIDDEN": "test"}})

	if res.Stdout != "step test absent\n" {
		t.Errorf("RunWorkflowScript(env).Stdout = %q; want %q, stderr %q", res.Stdout, "step test absent\n", res.Stderr)
	}
}

func TestRunWorkflowScript_RunsInTheDirectoryTheTestGives(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "version"), []byte("1.2.3\n"), 0o644); err != nil {
		t.Fatalf("writing the version file: %v", err)
	}
	step := kittest.WorkflowScript{Run: "read -r v < version\necho \"v$v\" > tag\n"}

	res := kittest.RunWorkflowScript(t, step, kittest.ScriptRun{Dir: dir})

	if res.ExitCode != 0 {
		t.Fatalf("RunWorkflowScript(read version).ExitCode = %d; want 0, stderr %q", res.ExitCode, res.Stderr)
	}
	tag, err := os.ReadFile(filepath.Join(dir, "tag"))
	if err != nil || string(tag) != "v1.2.3\n" {
		t.Errorf("tag file = %q, %v; want %q", tag, err, "v1.2.3\n")
	}
}
