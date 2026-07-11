package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()

	cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func lowerStep() transformStep {
	return transformStep{kind: transformLower}
}

func underscoreStep() transformStep {
	return transformStep{kind: transformUnderscores}
}

func numberingStep(width, next int, separator string) transformStep {
	return transformStep{
		kind:      transformNumbering,
		numbering: &numberingOptions{width: width, next: next, separator: separator},
	}
}

func runRenamePaths(paths []string, opts options) (summary, error) {
	var numbering *numberingState
	if opts.numbering != nil {
		numbering = &numberingState{next: opts.numbering.next}
	}
	plans, total, err := collectRenamePlans(paths, opts, numbering)
	if err != nil {
		return total, err
	}
	current, err := executeRenamePlans(plans, opts)
	total.add(current)
	return total, err
}

func TestParseExpressionApply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		expr  string
		input string
		want  string
	}{
		{
			name:  "single replacement by default",
			expr:  "s/foo/bar/",
			input: "foo foo",
			want:  "bar foo",
		},
		{
			name:  "global replacement",
			expr:  "s/foo/bar/g",
			input: "foo foo",
			want:  "bar bar",
		},
		{
			name:  "case insensitive replacement",
			expr:  "s/img/photo/i",
			input: "IMG_001",
			want:  "photo_001",
		},
		{
			name:  "alternate delimiter",
			expr:  "s#foo/bar#baz#",
			input: "foo/bar.txt",
			want:  "baz.txt",
		},
		{
			name:  "capturing group replacement",
			expr:  `s/(IMG)_([0-9]+)/\1-\2/`,
			input: "IMG_1234",
			want:  "IMG-1234",
		},
		{
			name:  "capture followed by letters",
			expr:  `s/(foo)/\1copy/`,
			input: "foo.txt",
			want:  "foocopy.txt",
		},
		{
			name:  "multi-digit capture followed by letters",
			expr:  `s/(a)(b)(c)(d)(e)(f)(g)(h)(i)(j)/\10copy/`,
			input: "abcdefghij.txt",
			want:  "jcopy.txt",
		},
		{
			name:  "escaped delimiter in pattern and replacement",
			expr:  `s#/foo/#/bar/#`,
			input: "/foo/file",
			want:  "/bar/file",
		},
		{
			name:  "unicode text",
			expr:  `s/Żółw/zolw/`,
			input: "Żółw.txt",
			want:  "zolw.txt",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, err := parseExpression(tc.expr)
			if err != nil {
				t.Fatalf("parseExpression(%q) error = %v", tc.expr, err)
			}

			got := r.Apply(tc.input)
			if got != tc.want {
				t.Fatalf("Apply(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseExpressionErrors(t *testing.T) {
	t.Parallel()

	tests := []string{
		"foo",
		"s/foo",
		"s/foo/bar/z",
	}

	for _, expr := range tests {
		expr := expr
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			if _, err := parseExpression(expr); err == nil {
				t.Fatalf("parseExpression(%q) succeeded, want error", expr)
			}
		})
	}
}

func TestNormalizeArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want []string
		ok   bool
	}{
		{
			name: "short flags allowed",
			args: []string{"-l", "-u", "-e", "s/foo/bar/", "-N", "01_", "-i"},
			want: []string{"-l", "-u", "-e", "s/foo/bar/", "-N", "01_", "-i"},
			ok:   true,
		},
		{
			name: "bundled short flags expand",
			args: []string{"-lrf"},
			want: []string{"-l", "-r", "-f"},
			ok:   true,
		},
		{
			name: "bundled short flags include underscores",
			args: []string{"-lru"},
			want: []string{"-l", "-r", "-u"},
			ok:   true,
		},
		{
			name: "double dash long flag allowed",
			args: []string{"--lower", "--underscores", "--no-color", "--number-prefix", "001_", "--help-long"},
			want: []string{"--lower", "--underscores", "--no-color", "--number-prefix", "001_", "--help-long"},
			ok:   true,
		},
		{
			name: "short boolean value allowed",
			args: []string{"-l=true", "-u=false"},
			want: []string{"-l=true", "-u=false"},
			ok:   true,
		},
		{
			name: "interactive long flag allowed",
			args: []string{"--interactive"},
			want: []string{"--interactive"},
			ok:   true,
		},
		{
			name: "single dash long flag rejected",
			args: []string{"-lower"},
			ok:   false,
		},
		{
			name: "single dash long flag with value rejected",
			args: []string{"-underscores"},
			ok:   false,
		},
		{
			name: "removed replace short flag rejected",
			args: []string{"-p", "foo=bar"},
			ok:   false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeArgs(tc.args)
			if tc.ok && err != nil {
				t.Fatalf("normalizeArgs(%v) error = %v", tc.args, err)
			}
			if tc.ok && strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
				t.Fatalf("normalizeArgs(%v) = %v, want %v", tc.args, got, tc.want)
			}
			if !tc.ok && err == nil {
				t.Fatalf("normalizeArgs(%v) succeeded, want error", tc.args)
			}
		})
	}
}

func TestFormatRenameWithoutColor(t *testing.T) {
	t.Parallel()

	got := formatRename("/tmp/FOO.txt", "/tmp/BAR.txt", false, false)
	want := "[rename] /tmp/FOO.txt -> /tmp/BAR.txt"
	if got != want {
		t.Fatalf("formatRename() = %q, want %q", got, want)
	}
}

func TestFormatRenameWithColorHighlightsChangedBasename(t *testing.T) {
	t.Parallel()

	got := formatRename("/tmp/prefixFOOsuffix.txt", "/tmp/prefixBARsuffix.txt", true, false)
	want := "[rename] /tmp/prefix" + ansiOld + "FOO" + ansiReset + "suffix.txt -> /tmp/prefix" + ansiNew + "BAR" + ansiReset + "suffix.txt"
	if got != want {
		t.Fatalf("formatRename() = %q, want %q", got, want)
	}
}

func TestFormatRenameWithColorFallsBackWhenDirectoryChanges(t *testing.T) {
	t.Parallel()

	got := formatRename("/tmp/old/file.txt", "/tmp/new/file.txt", true, false)
	want := "[rename] /tmp/old/file.txt -> /tmp/new/file.txt"
	if got != want {
		t.Fatalf("formatRename() = %q, want %q", got, want)
	}
}

func TestFormatRenameDryRunUsesPlanVerb(t *testing.T) {
	t.Parallel()

	got := formatRename("/tmp/old.txt", "/tmp/new.txt", false, true)
	want := "[plan] /tmp/old.txt -> /tmp/new.txt"
	if got != want {
		t.Fatalf("formatRename() = %q, want %q", got, want)
	}
}

func TestFormatError(t *testing.T) {
	t.Parallel()

	err := formatError("%s: %s", "foo", "bar")
	if err.Error() != "error: foo: bar" {
		t.Fatalf("formatError() = %q", err.Error())
	}
}

func TestColorModeValidation(t *testing.T) {
	t.Parallel()

	if !isValidColorMode(colorModeAuto) || !isValidColorMode(colorModeAlways) || !isValidColorMode(colorModeNever) {
		t.Fatal("expected standard color modes to be valid")
	}
	if isValidColorMode("sometimes") {
		t.Fatal("unexpected valid color mode")
	}
}

func TestSupports256Color(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM_PROGRAM_VERSION", "")
	t.Setenv("COLORS", "")

	t.Run("term 256color", func(t *testing.T) {
		t.Setenv("TERM", "xterm-256color")
		t.Setenv("COLORTERM", "")
		if !supports256Color() {
			t.Fatal("expected TERM with 256color to enable palette")
		}
	})

	t.Run("colorterm truecolor", func(t *testing.T) {
		t.Setenv("TERM", "xterm")
		t.Setenv("COLORTERM", "truecolor")
		if !supports256Color() {
			t.Fatal("expected truecolor terminal to enable palette")
		}
	})

	t.Run("colors env fallback", func(t *testing.T) {
		t.Setenv("TERM", "xterm")
		t.Setenv("COLORTERM", "")
		t.Setenv("COLORS", "256")
		if !supports256Color() {
			t.Fatal("expected COLORS=256 to enable palette")
		}
	})

	t.Run("plain term disabled", func(t *testing.T) {
		t.Setenv("TERM", "xterm")
		t.Setenv("COLORTERM", "")
		t.Setenv("COLORS", "")
		if supports256Color() {
			t.Fatal("expected plain term to disable palette")
		}
	})
}

func TestShouldUseColorEnv(t *testing.T) {
	t.Parallel()

	env := func(values map[string]string) func(string) string {
		return func(key string) string {
			return values[key]
		}
	}

	if !shouldUseColorEnv(colorModeAlways, false, env(map[string]string{})) {
		t.Fatal("expected always mode to enable color")
	}
	if shouldUseColorEnv(colorModeNever, true, env(map[string]string{"TERM": "xterm-256color"})) {
		t.Fatal("expected never mode to disable color")
	}
	if shouldUseColorEnv(colorModeAuto, false, env(map[string]string{"TERM": "xterm-256color"})) {
		t.Fatal("expected auto mode to disable color on non-tty")
	}
	if !shouldUseColorEnv(colorModeAuto, true, env(map[string]string{"TERM": "xterm-256color"})) {
		t.Fatal("expected auto mode to enable color on tty with 256 colors")
	}
	if shouldUseColorEnv(colorModeAuto, true, env(map[string]string{"TERM": "dumb"})) {
		t.Fatal("expected dumb terminal to disable color")
	}
}

func TestParseNumbering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input     string
		wantWidth int
		wantNext  int
		wantSep   string
		ok        bool
	}{
		{input: "001_", wantWidth: 3, wantNext: 1, wantSep: "_", ok: true},
		{input: "002_", wantWidth: 3, wantNext: 2, wantSep: "_", ok: true},
		{input: "01", wantWidth: 2, wantNext: 1, wantSep: "", ok: true},
		{input: "7-", wantWidth: 1, wantNext: 7, wantSep: "-", ok: true},
		{input: "", ok: false},
		{input: "_", ok: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()

			got, err := parseNumbering(tc.input)
			if tc.ok && err != nil {
				t.Fatalf("parseNumbering(%q) error = %v", tc.input, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("parseNumbering(%q) succeeded, want error", tc.input)
			}
			if tc.ok && (got.width != tc.wantWidth || got.next != tc.wantNext || got.separator != tc.wantSep) {
				t.Fatalf("parseNumbering(%q) = %+v", tc.input, got)
			}
		})
	}
}

func TestCollectTransformsRespectsOrder(t *testing.T) {
	t.Parallel()

	first, _, err := collectTransforms([]string{"-e", "s/abyss/Abyss/g", "-l"})
	if err != nil {
		t.Fatalf("collectTransforms first: %v", err)
	}
	second, _, err := collectTransforms([]string{"-l", "-e", "s/abyss/Abyss/g"})
	if err != nil {
		t.Fatalf("collectTransforms second: %v", err)
	}

	optsFirst := options{transforms: first}
	optsSecond := options{transforms: second}
	if got := applyTransforms("abyss file.txt", optsFirst, 0); got != "abyss file.txt" {
		t.Fatalf("applyTransforms first = %q", got)
	}
	if got := applyTransforms("abyss file.txt", optsSecond, 0); got != "Abyss file.txt" {
		t.Fatalf("applyTransforms second = %q", got)
	}
}

func TestCollectTransformsHonorsExplicitBooleanValues(t *testing.T) {
	t.Parallel()

	steps, _, err := collectTransforms([]string{
		"--lower=false",
		"--underscores=true",
		"-l=true",
		"-u=false",
	})
	if err != nil {
		t.Fatalf("collectTransforms: %v", err)
	}
	if len(steps) != 2 || steps[0].kind != transformUnderscores || steps[1].kind != transformLower {
		t.Fatalf("collectTransforms steps = %+v", steps)
	}
}

func TestCLIExplicitBooleanTransformValue(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "README")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	out, err := runCLI(t, "--dry-run", "--lower=true", "--underscores=false", target)
	if err != nil {
		t.Fatalf("runCLI: %v\n%s", err, out)
	}
	if !strings.Contains(out, target+" -> "+filepath.Join(dir, "readme")) {
		t.Fatalf("explicit boolean output = %s", out)
	}
}

func TestApplyTransformsOrderWithNumbering(t *testing.T) {
	t.Parallel()

	first, numberingFirst, err := collectTransforms([]string{"-N", "01_", "-e", "s/^01_/x_/"})
	if err != nil {
		t.Fatalf("collectTransforms first: %v", err)
	}
	second, numberingSecond, err := collectTransforms([]string{"-e", "s/^01_/x_/", "-N", "01_"})
	if err != nil {
		t.Fatalf("collectTransforms second: %v", err)
	}

	optsFirst := options{transforms: first, numbering: numberingFirst}
	optsSecond := options{transforms: second, numbering: numberingSecond}
	if got := applyTransforms("Photo.JPG", optsFirst, 1); got != "x_Photo.JPG" {
		t.Fatalf("applyTransforms first = %q", got)
	}
	if got := applyTransforms("Photo.JPG", optsSecond, 1); got != "01_Photo.JPG" {
		t.Fatalf("applyTransforms second = %q", got)
	}
}

func TestRecursiveNumberingCombinationRejected(t *testing.T) {
	numbering, err := parseNumbering("001_")
	if err != nil {
		t.Fatalf("parseNumbering: %v", err)
	}
	if err := validateFlagCombination(false, false, true, numbering); err == nil {
		t.Fatal("validateFlagCombination succeeded, want error")
	}
	if err := validateFlagCombination(true, true, false, nil); err == nil {
		t.Fatal("validateFlagCombination for files/dirs succeeded, want error")
	}
	if err := validateFlagCombination(false, false, false, numbering); err != nil {
		t.Fatalf("validateFlagCombination non-recursive error = %v", err)
	}
}

func TestExecuteRenamePlansAppliesTransforms(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "Foo Bar.JPG")
	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := runRenamePaths([]string{oldPath}, options{
		transforms: []transformStep{lowerStep(), underscoreStep()},
	})
	if err != nil {
		t.Fatalf("runRenamePaths: %v", err)
	}
	if result.renamed != 1 || result.planned != 0 || result.skipped != 0 || result.errors != 0 {
		t.Fatalf("runRenamePaths summary = %+v", result)
	}

	newPath := filepath.Join(dir, "foo_bar.jpg")
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("Stat(%q): %v", newPath, err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old path still exists or unexpected error: %v", err)
	}
}

func TestRecursivePlanRenamesChildrenBeforeParents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	oldDir := filepath.Join(root, "UPPER DIR")
	oldFile := filepath.Join(oldDir, "IMG 2024.jpeg")

	if err := os.Mkdir(oldDir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(oldFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	paths, err := collectPaths([]string{root}, true)
	if err != nil {
		t.Fatalf("collectPaths: %v", err)
	}
	plans, baseSummary, err := collectRenamePlans(paths, options{
		transforms: []transformStep{lowerStep(), underscoreStep()},
	}, nil)
	if err != nil {
		t.Fatalf("collectRenamePlans: %v", err)
	}
	result, err := executeRenamePlans(plans, options{})
	result.add(baseSummary)
	if result.renamed != 2 || result.planned != 0 || result.errors != 0 {
		t.Fatalf("recursive summary = %+v", result)
	}

	newDir := filepath.Join(root, "upper_dir")
	newFile := filepath.Join(newDir, "img_2024.jpeg")
	if _, err := os.Stat(newDir); err != nil {
		t.Fatalf("Stat(%q): %v", newDir, err)
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Fatalf("Stat(%q): %v", newFile, err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old dir still exists or unexpected error: %v", err)
	}
}

func TestExecuteRenamePlansRejectsExistingTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "Foo")
	newPath := filepath.Join(dir, "target")

	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile old: %v", err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o644); err != nil {
		t.Fatalf("WriteFile new: %v", err)
	}

	r, err := parseExpression(`s/^Foo$/target/`)
	if err != nil {
		t.Fatalf("parseExpression: %v", err)
	}
	result, err := runRenamePaths([]string{oldPath}, options{transforms: []transformStep{{kind: transformRegex, replacer: r}}})
	if err == nil {
		t.Fatal("runRenamePaths succeeded, want error")
	}
	if result.renamed != 0 {
		t.Fatalf("runRenamePaths summary = %+v", result)
	}
	if !strings.Contains(err.Error(), "target exists") {
		t.Fatalf("runRenamePaths error = %v, want target exists", err)
	}
}

func TestExecuteRenamePlansReplacesWhitespaceRuns(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "  Foo   Bar  .txt  ")
	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := runRenamePaths([]string{oldPath}, options{transforms: []transformStep{underscoreStep()}})
	if err != nil {
		t.Fatalf("runRenamePaths: %v", err)
	}
	if result.renamed != 1 {
		t.Fatalf("runRenamePaths summary = %+v", result)
	}

	newPath := filepath.Join(dir, "_Foo_Bar_.txt_")
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("Stat(%q): %v", newPath, err)
	}
}

func TestExecuteRenamePlansFilesOnlySkipsDirectories(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	targetDir := filepath.Join(dir, "Folder Name")
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	result, err := runRenamePaths([]string{targetDir}, options{
		filesOnly:  true,
		transforms: []transformStep{underscoreStep()},
	})
	if err != nil {
		t.Fatalf("runRenamePaths: %v", err)
	}
	if result.skipped != 1 {
		t.Fatalf("runRenamePaths summary = %+v", result)
	}

	if _, err := os.Stat(targetDir); err != nil {
		t.Fatalf("Stat(%q): %v", targetDir, err)
	}
}

func TestExecuteRenamePlansNumberPrefix(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "Photo.JPG")
	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	numbering := &numberingOptions{width: 3, next: 1, separator: "_"}
	result, err := runRenamePaths([]string{oldPath}, options{
		transforms: []transformStep{numberingStep(3, 1, "_")},
		numbering:  numbering,
	})
	if err != nil {
		t.Fatalf("runRenamePaths: %v", err)
	}
	if result.renamed != 1 {
		t.Fatalf("runRenamePaths summary = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "001_Photo.JPG")); err != nil {
		t.Fatalf("Stat numbered file: %v", err)
	}
}

func TestExecuteRenamePlansNumberPrefixWithoutSeparator(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "foo.txt")
	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := runRenamePaths([]string{oldPath}, options{
		transforms: []transformStep{numberingStep(2, 1, "")},
		numbering:  &numberingOptions{width: 2, next: 1, separator: ""},
	})
	if err != nil {
		t.Fatalf("runRenamePaths: %v", err)
	}
	if result.renamed != 1 {
		t.Fatalf("runRenamePaths summary = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "01foo.txt")); err != nil {
		t.Fatalf("Stat numbered file: %v", err)
	}
}

func TestBuildNumberedPlansFailsBeforeStarting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oldOne := filepath.Join(dir, "one.txt")
	oldTwo := filepath.Join(dir, "two.txt")
	target := filepath.Join(dir, "01_one.txt")

	for _, path := range []string{oldOne, oldTwo, target} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}

	numbering := &numberingState{next: 1}
	plans, skipped, err := buildNumberedPlans([]string{oldOne, oldTwo}, options{
		transforms: []transformStep{numberingStep(2, 1, "_")},
		numbering:  &numberingOptions{width: 2, separator: "_"},
	}, numbering)
	if err == nil {
		t.Fatal("buildNumberedPlans succeeded, want error")
	}
	if len(plans) != 0 || skipped != 0 {
		t.Fatalf("buildNumberedPlans returned plans=%v skipped=%d", plans, skipped)
	}
	if numbering.next != 1 {
		t.Fatalf("numbering.next = %d, want unchanged 1", numbering.next)
	}
	if _, err := os.Stat(oldOne); err != nil {
		t.Fatalf("Stat oldOne: %v", err)
	}
	if _, err := os.Stat(oldTwo); err != nil {
		t.Fatalf("Stat oldTwo: %v", err)
	}
}

func TestBuildNumberedPlansSkipsUnchangedWithoutAdvancing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	skippedDir := filepath.Join(dir, "ignored dir")
	changed := filepath.Join(dir, "Needs Space.txt")
	if err := os.Mkdir(skippedDir, 0o755); err != nil {
		t.Fatalf("Mkdir(%q): %v", skippedDir, err)
	}
	if err := os.WriteFile(changed, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", changed, err)
	}
	r, err := parseExpression(`s/^Needs //`)
	if err != nil {
		t.Fatalf("parseExpression: %v", err)
	}

	numbering := &numberingState{next: 1}
	plans, skipped, err := buildNumberedPlans([]string{skippedDir, changed}, options{
		filesOnly: true,
		transforms: []transformStep{
			{kind: transformRegex, replacer: r},
			numberingStep(2, 1, "_"),
		},
		numbering: &numberingOptions{width: 2, separator: "_"},
	}, numbering)
	if err != nil {
		t.Fatalf("buildNumberedPlans: %v", err)
	}
	if skipped != 1 || len(plans) != 1 {
		t.Fatalf("buildNumberedPlans got skipped=%d plans=%v", skipped, plans)
	}
	if got := filepath.Base(plans[0].newPath); got != "01_Space.txt" {
		t.Fatalf("buildNumberedPlans new path = %q", got)
	}
	if numbering.next != 2 {
		t.Fatalf("numbering.next = %d, want 2", numbering.next)
	}
}

func TestValidateEditedPlansAllowsSwapTargets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.WriteFile(a, []byte("A"), 0o644); err != nil {
		t.Fatalf("WriteFile a: %v", err)
	}
	if err := os.WriteFile(b, []byte("B"), 0o644); err != nil {
		t.Fatalf("WriteFile b: %v", err)
	}
	plans, skipped, err := validateEditedPlans([]renamePlan{
		{oldPath: a, newPath: b},
		{oldPath: b, newPath: a},
	})
	if err != nil {
		t.Fatalf("validateEditedPlans: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("validateEditedPlans skipped = %d, want 0", skipped)
	}
	if len(plans) != 2 {
		t.Fatalf("validateEditedPlans len = %d, want 2", len(plans))
	}
}

func TestValidateEditedPlansRejectsTargetingUnchangedSource(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.WriteFile(a, []byte("A"), 0o644); err != nil {
		t.Fatalf("WriteFile a: %v", err)
	}
	if err := os.WriteFile(b, []byte("B"), 0o644); err != nil {
		t.Fatalf("WriteFile b: %v", err)
	}

	_, _, err := validateEditedPlans([]renamePlan{
		{oldPath: a, newPath: a},
		{oldPath: b, newPath: a},
	})
	if err == nil {
		t.Fatal("validateEditedPlans succeeded, want error")
	}
	if !strings.Contains(err.Error(), "target exists") {
		t.Fatalf("validateEditedPlans error = %v", err)
	}
}

func TestExecuteRenamePlansSwapsFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(a, []byte("A"), 0o644); err != nil {
		t.Fatalf("WriteFile a: %v", err)
	}
	if err := os.WriteFile(b, []byte("B"), 0o644); err != nil {
		t.Fatalf("WriteFile b: %v", err)
	}

	result, err := executeRenamePlans([]renamePlan{
		{oldPath: a, newPath: b},
		{oldPath: b, newPath: a},
	}, options{})
	if err != nil {
		t.Fatalf("executeRenamePlans: %v", err)
	}
	if result.renamed != 2 {
		t.Fatalf("executeRenamePlans summary = %+v", result)
	}

	dataA, err := os.ReadFile(a)
	if err != nil {
		t.Fatalf("ReadFile a: %v", err)
	}
	dataB, err := os.ReadFile(b)
	if err != nil {
		t.Fatalf("ReadFile b: %v", err)
	}
	if string(dataA) != "B" || string(dataB) != "A" {
		t.Fatalf("swap contents = %q/%q, want B/A", string(dataA), string(dataB))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".rrtmp-") {
			t.Fatalf("temporary rename path left behind: %s", entry.Name())
		}
	}
}

func TestRenameNoReplacePreservesExistingTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(source, []byte("source"), 0o644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	if err := os.WriteFile(target, []byte("target"), 0o644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}

	if err := renameNoReplace(source, target); err == nil {
		t.Fatal("renameNoReplace succeeded with an existing target")
	}
	for path, want := range map[string]string{source: "source", target: "target"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", path, err)
		}
		if string(data) != want {
			t.Fatalf("ReadFile(%q) = %q, want %q", path, data, want)
		}
	}
}

func TestExecuteRenamePlansRejectsTargetCreatedAfterValidation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(source, []byte("source"), 0o644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	plans := []renamePlan{{oldPath: source, newPath: target}}
	if err := validateRenamePlans(plans); err != nil {
		t.Fatalf("validateRenamePlans: %v", err)
	}
	if err := os.WriteFile(target, []byte("target"), 0o644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}

	result, err := executeRenamePlans(plans, options{})
	if err == nil {
		t.Fatal("executeRenamePlans succeeded after target appeared")
	}
	if result.renamed != 0 || !strings.Contains(err.Error(), "target exists") {
		t.Fatalf("executeRenamePlans result=%+v err=%v", result, err)
	}
	for path, want := range map[string]string{source: "source", target: "target"} {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("ReadFile(%q): %v", path, readErr)
		}
		if string(data) != want {
			t.Fatalf("ReadFile(%q) = %q, want %q", path, data, want)
		}
	}
}

func TestExecuteRenamePlansRollsBackCompletedMoves(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	one := filepath.Join(dir, "one")
	two := filepath.Join(dir, "two")
	if err := os.WriteFile(one, []byte("one"), 0o644); err != nil {
		t.Fatalf("WriteFile one: %v", err)
	}
	if err := os.WriteFile(two, []byte("two"), 0o644); err != nil {
		t.Fatalf("WriteFile two: %v", err)
	}

	calls := 0
	injected := errors.New("injected move failure")
	move := func(oldPath, newPath string) error {
		calls++
		if calls == 2 {
			return injected
		}
		return renameNoReplace(oldPath, newPath)
	}
	result, err := executeRenamePlansWith([]renamePlan{
		{oldPath: one, newPath: filepath.Join(dir, "one-new")},
		{oldPath: two, newPath: filepath.Join(dir, "two-new")},
	}, options{}, move)
	if err == nil || !errors.Is(err, injected) {
		t.Fatalf("executeRenamePlansWith error = %v, want injected failure", err)
	}
	if result.renamed != 0 || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("executeRenamePlansWith result=%+v err=%v", result, err)
	}
	for _, path := range []string{one, two} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("source %q not restored: %v", path, statErr)
		}
	}
	for _, path := range []string{filepath.Join(dir, "one-new"), filepath.Join(dir, "two-new")} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("target %q remains after rollback: %v", path, statErr)
		}
	}
}

func TestExecuteRenamePlansReportsRollbackFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	one := filepath.Join(dir, "one")
	two := filepath.Join(dir, "two")
	if err := os.WriteFile(one, []byte("one"), 0o644); err != nil {
		t.Fatalf("WriteFile one: %v", err)
	}
	if err := os.WriteFile(two, []byte("two"), 0o644); err != nil {
		t.Fatalf("WriteFile two: %v", err)
	}

	calls := 0
	move := func(oldPath, newPath string) error {
		calls++
		if calls == 2 {
			return errors.New("forward failure")
		}
		if calls == 3 {
			return errors.New("rollback failure")
		}
		return renameNoReplace(oldPath, newPath)
	}
	_, err := executeRenamePlansWith([]renamePlan{
		{oldPath: one, newPath: filepath.Join(dir, "one-new")},
		{oldPath: two, newPath: filepath.Join(dir, "two-new")},
	}, options{}, move)
	if err == nil || !strings.Contains(err.Error(), "rollback incomplete") || !strings.Contains(err.Error(), "rollback failure") {
		t.Fatalf("executeRenamePlansWith error = %v", err)
	}
}

func TestValidateRenamePlansAllowsChainedTargets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.WriteFile(a, []byte("A"), 0o644); err != nil {
		t.Fatalf("WriteFile a: %v", err)
	}
	if err := os.WriteFile(b, []byte("B"), 0o644); err != nil {
		t.Fatalf("WriteFile b: %v", err)
	}

	err := validateRenamePlans([]renamePlan{
		{oldPath: a, newPath: b},
		{oldPath: b, newPath: filepath.Join(dir, "c")},
	})
	if err != nil {
		t.Fatalf("validateRenamePlans: %v", err)
	}
}

func TestValidateRenamePlansRejectsDuplicateTargets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	target := filepath.Join(dir, "same")
	for _, path := range []string{a, b} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}

	err := validateRenamePlans([]renamePlan{
		{oldPath: a, newPath: target},
		{oldPath: b, newPath: target},
	})
	if err == nil {
		t.Fatal("validateRenamePlans succeeded, want error")
	}
	if !strings.Contains(err.Error(), "target exists") {
		t.Fatalf("validateRenamePlans error = %v", err)
	}
}

func TestValidateRenamePlansRejectsExistingUnmovedTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	for _, path := range []string{a, b} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}

	err := validateRenamePlans([]renamePlan{
		{oldPath: a, newPath: b},
	})
	if err == nil {
		t.Fatal("validateRenamePlans succeeded, want error")
	}
	if !strings.Contains(err.Error(), "target exists") {
		t.Fatalf("validateRenamePlans error = %v", err)
	}
}

func TestTargetPathConflictsTreatsHardLinkAsConflict(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "source")
	hardLinkPath := filepath.Join(dir, "hard-link")
	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile old: %v", err)
	}
	if err := os.Link(oldPath, hardLinkPath); err != nil {
		t.Fatalf("Link: %v", err)
	}
	oldInfo, err := os.Lstat(oldPath)
	if err != nil {
		t.Fatalf("Lstat old: %v", err)
	}

	conflict, err := targetPathConflicts(oldPath, oldInfo, hardLinkPath)
	if err != nil {
		t.Fatalf("targetPathConflicts: %v", err)
	}
	if !conflict {
		t.Fatal("targetPathConflicts = false, want true for distinct hard link")
	}
}

func TestTargetPathConflictsAllowsIdenticalPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "Foo")
	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile old: %v", err)
	}
	oldInfo, err := os.Lstat(oldPath)
	if err != nil {
		t.Fatalf("Lstat old: %v", err)
	}

	conflict, err := targetPathConflicts(oldPath, oldInfo, oldPath)
	if err != nil {
		t.Fatalf("targetPathConflicts: %v", err)
	}
	if conflict {
		t.Fatal("targetPathConflicts = true, want false for same path")
	}
}

func TestValidateRenamePlansRejectsDuplicateSources(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile a: %v", err)
	}

	err := validateRenamePlans([]renamePlan{
		{oldPath: a, newPath: filepath.Join(dir, "b")},
		{oldPath: a, newPath: filepath.Join(dir, "c")},
	})
	if err == nil {
		t.Fatal("validateRenamePlans succeeded, want error")
	}
	if !strings.Contains(err.Error(), "duplicate source path") {
		t.Fatalf("validateRenamePlans error = %v", err)
	}
}

func TestValidateRenamePlansRejectsSourceAliases(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	intermediate := filepath.Join(dir, "intermediate")
	if err := os.Mkdir(intermediate, 0o755); err != nil {
		t.Fatalf("Mkdir intermediate: %v", err)
	}
	source := filepath.Join(dir, "Foo Name")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	alias := filepath.Join(intermediate, "..", "Foo Name")
	err := validateRenamePlans([]renamePlan{
		{oldPath: source, newPath: filepath.Join(dir, "first")},
		{oldPath: alias, newPath: filepath.Join(dir, "second")},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate source path") {
		t.Fatalf("validateRenamePlans error = %v, want duplicate source", err)
	}
}

func TestValidateRenamePlansRejectsSymlinkedParentAlias(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	linkDir := filepath.Join(dir, "link")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatalf("Mkdir real: %v", err)
	}
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("filesystem does not support directory symlinks: %v", err)
	}
	source := filepath.Join(realDir, "source")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	err := validateRenamePlans([]renamePlan{
		{oldPath: source, newPath: filepath.Join(realDir, "first")},
		{oldPath: filepath.Join(linkDir, "source"), newPath: filepath.Join(linkDir, "second")},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate source path") {
		t.Fatalf("validateRenamePlans error = %v, want duplicate source", err)
	}
}

func TestValidateRenamePlansKeepsDistinctHardLinksDistinct(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")
	if err := os.WriteFile(first, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile first: %v", err)
	}
	if err := os.Link(first, second); err != nil {
		t.Skipf("filesystem does not support hard links: %v", err)
	}
	if err := validateRenamePlans([]renamePlan{
		{oldPath: first, newPath: filepath.Join(dir, "first-new")},
		{oldPath: second, newPath: filepath.Join(dir, "second-new")},
	}); err != nil {
		t.Fatalf("validateRenamePlans rejected distinct hard links: %v", err)
	}
}

func TestCollectRenamePlansRejectsPathSeparatorBeforeMutation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first := filepath.Join(dir, "A")
	second := filepath.Join(dir, "B")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte(filepath.Base(path)), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}
	toLower, err := parseExpression(`s/^A$/a/`)
	if err != nil {
		t.Fatalf("parse first expression: %v", err)
	}
	toMissingDir, err := parseExpression(`s#^B$#missing/B#`)
	if err != nil {
		t.Fatalf("parse second expression: %v", err)
	}
	_, _, err = collectRenamePlans([]string{first, second}, options{transforms: []transformStep{
		{kind: transformRegex, replacer: toLower},
		{kind: transformRegex, replacer: toMissingDir},
	}}, nil)
	if err == nil || !strings.Contains(err.Error(), "path separator") {
		t.Fatalf("collectRenamePlans error = %v, want path separator", err)
	}
	for _, path := range []string{first, second} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("source changed after invalid plan: %v", statErr)
		}
	}
}

func TestValidateEditedPlansRejectsDirectoryChange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	other := filepath.Join(dir, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatalf("Mkdir other: %v", err)
	}
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	_, _, err := validateEditedPlans([]renamePlan{{oldPath: source, newPath: filepath.Join(other, "target")}})
	if err == nil || !strings.Contains(err.Error(), "target must remain in source directory") {
		t.Fatalf("validateEditedPlans error = %v", err)
	}
}

func TestEditPlansRemovesTemporaryFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	var planPath string
	edited, err := editPlansWith([]renamePlan{{oldPath: source, newPath: target}}, func(path string) error {
		planPath = path
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("temporary plan unavailable to editor: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("editPlansWith: %v", err)
	}
	if len(edited) != 1 || edited[0].oldPath != source || edited[0].newPath != target {
		t.Fatalf("editPlansWith result = %+v", edited)
	}
	if _, err := os.Stat(planPath); !os.IsNotExist(err) {
		t.Fatalf("temporary plan still exists after editing: %v", err)
	}
}

func TestEditPlansRemovesTemporaryFileAfterEditorError(t *testing.T) {
	t.Parallel()

	var planPath string
	injected := errors.New("editor failed")
	_, err := editPlansWith(nil, func(path string) error {
		planPath = path
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("editPlansWith error = %v, want injected error", err)
	}
	if _, err := os.Stat(planPath); !os.IsNotExist(err) {
		t.Fatalf("temporary plan still exists after editor error: %v", err)
	}
}

func TestRunInteractiveSupportsEditThenYes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	firstTarget := filepath.Join(dir, "first")
	secondTarget := filepath.Join(dir, "second")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	calls := 0
	edit := func(current []renamePlan) ([]renamePlan, error) {
		calls++
		if calls == 1 {
			return []renamePlan{{oldPath: source, newPath: firstTarget}}, nil
		}
		if len(current) != 1 || current[0].newPath != firstTarget {
			t.Fatalf("second edit received %+v, want first edited plan", current)
		}
		return []renamePlan{{oldPath: source, newPath: secondTarget}}, nil
	}
	got, _, err := runInteractiveWith(
		[]renamePlan{{oldPath: source, newPath: source}},
		options{},
		edit,
		bufio.NewReader(strings.NewReader("edit\nyes\n")),
	)
	if err != nil {
		t.Fatalf("runInteractiveWith: %v", err)
	}
	if calls != 2 || len(got) != 1 || got[0].newPath != secondTarget {
		t.Fatalf("runInteractiveWith calls=%d plans=%+v", calls, got)
	}
}

func TestRunInteractiveCancel(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	_, _, err := runInteractiveWith(
		[]renamePlan{{oldPath: source, newPath: target}},
		options{},
		func(current []renamePlan) ([]renamePlan, error) { return current, nil },
		bufio.NewReader(strings.NewReader("cancel\n")),
	)
	if err == nil || err.Error() != "canceled" {
		t.Fatalf("runInteractiveWith error = %v, want canceled", err)
	}
}

func TestRunInteractiveReopensInvalidPlan(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	otherDir := filepath.Join(dir, "other")
	if err := os.Mkdir(otherDir, 0o755); err != nil {
		t.Fatalf("Mkdir other: %v", err)
	}
	source := filepath.Join(dir, "source")
	validTarget := filepath.Join(dir, "target")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	calls := 0
	edit := func(current []renamePlan) ([]renamePlan, error) {
		calls++
		if calls == 1 {
			return []renamePlan{{oldPath: source, newPath: filepath.Join(otherDir, "invalid")}}, nil
		}
		return []renamePlan{{oldPath: source, newPath: validTarget}}, nil
	}
	got, _, err := runInteractiveWith(
		[]renamePlan{{oldPath: source, newPath: validTarget}},
		options{},
		edit,
		bufio.NewReader(strings.NewReader("yes\n")),
	)
	if err != nil {
		t.Fatalf("runInteractiveWith: %v", err)
	}
	if calls != 2 || len(got) != 1 || got[0].newPath != validTarget {
		t.Fatalf("runInteractiveWith calls=%d plans=%+v", calls, got)
	}
}

func TestParseEditedPlansRejectsChangedSource(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "plan.txt")
	original := []renamePlan{{oldPath: filepath.Join(dir, "source"), newPath: filepath.Join(dir, "target")}}
	data := fmt.Sprintf("%q\t%q\n", filepath.Join(dir, "other"), original[0].newPath)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile plan: %v", err)
	}
	if _, err := parseEditedPlans(path, original); err == nil || !strings.Contains(err.Error(), "changed or reordered old paths") {
		t.Fatalf("parseEditedPlans error = %v", err)
	}
}

func TestExecuteRenamePlansPerformsCaseOnlyRename(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "Foo")
	newPath := filepath.Join(dir, "foo")
	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile old: %v", err)
	}
	oldInfo, err := os.Lstat(oldPath)
	if err != nil {
		t.Fatalf("Lstat old: %v", err)
	}
	newInfo, err := os.Lstat(newPath)
	if err != nil || !os.SameFile(oldInfo, newInfo) {
		t.Skip("temp filesystem is case-sensitive")
	}

	result, err := runRenamePaths([]string{oldPath}, options{transforms: []transformStep{lowerStep()}})
	if err != nil {
		t.Fatalf("runRenamePaths: %v", err)
	}
	if result.renamed != 1 {
		t.Fatalf("runRenamePaths summary = %+v", result)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "foo" {
		t.Fatalf("case-only rename entries = %v, want foo", entries)
	}
}

func TestExecuteRenamePlansNumberingChangesNothingOnError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "Photo.JPG")
	targetPath := filepath.Join(dir, "001_Photo.JPG")
	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile old: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("y"), 0o644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}

	numbering := &numberingOptions{width: 3, next: 1, separator: "_"}
	result, err := runRenamePaths([]string{oldPath}, options{
		transforms: []transformStep{numberingStep(3, 1, "_")},
		numbering:  numbering,
	})
	if err == nil {
		t.Fatal("runRenamePaths succeeded, want error")
	}
	if result.renamed != 0 {
		t.Fatalf("runRenamePaths summary = %+v", result)
	}
	if numbering.next != 1 {
		t.Fatalf("numbering.next = %d, want unchanged 1", numbering.next)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("source changed after failed numbered plan: %v", err)
	}
}

func TestExecuteRenamePlanDryRunCountsPlanned(t *testing.T) {
	t.Parallel()

	result, err := executeRenamePlans([]renamePlan{{
		oldPath: "/tmp/old.txt",
		newPath: "/tmp/new.txt",
	}}, options{dryRun: true})
	if err != nil {
		t.Fatalf("executeRenamePlans: %v", err)
	}
	if result.planned != 1 || result.renamed != 0 {
		t.Fatalf("executeRenamePlans summary = %+v", result)
	}
}

func TestExecuteRenamePlansDryRunCountsAllPlanned(t *testing.T) {
	t.Parallel()

	result, err := executeRenamePlans([]renamePlan{
		{oldPath: "/tmp/one", newPath: "/tmp/ONE"},
		{oldPath: "/tmp/two", newPath: "/tmp/TWO"},
	}, options{dryRun: true})
	if err != nil {
		t.Fatalf("executeRenamePlans: %v", err)
	}
	if result.planned != 2 || result.renamed != 0 || result.errors != 0 {
		t.Fatalf("executeRenamePlans summary = %+v", result)
	}
}

func TestCLIHelpLong(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("go", "run", ".", "--help-long")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("help-long failed: %v\n%s", err, out)
	}
	text := string(out)
	if !strings.Contains(text, "Examples:") {
		t.Fatalf("help-long missing examples:\n%s", text)
	}
}

func TestCLIRejectsRecursiveNumbering(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("go", "run", ".", "--dry-run", "-r", "-N", "001_", ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("recursive numbering unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(string(out), "cannot combine --recursive with --number-prefix") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestCLIDoubleDashStopsFlagParsing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "--LOOKS-LIKE-FLAG")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out, err := runCLI(t, "--dry-run", "-l", "--", target)
	if err != nil {
		t.Fatalf("double-dash run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "[plan] "+target+" -> "+filepath.Join(dir, "--looks-like-flag")) {
		t.Fatalf("double-dash output missing plan:\n%s", out)
	}
	if !strings.Contains(out, "[summary] planned: 1, skipped: 0, errors: 0") {
		t.Fatalf("double-dash output = %s", out)
	}
}

func TestCLIRejectsDuplicateNumberPrefixFlags(t *testing.T) {
	t.Parallel()

	out, err := runCLI(t, "--dry-run", "-N", "01_", "--number-prefix", "02_", ".")
	if err == nil {
		t.Fatalf("duplicate number-prefix unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(out, "cannot specify --number-prefix more than once") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestCLIConflictingRecursivePlanChangesNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Foo"), []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile Foo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Bar"), []byte("other"), 0o644); err != nil {
		t.Fatalf("WriteFile Bar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "same"), []byte("target"), 0o644); err != nil {
		t.Fatalf("WriteFile same: %v", err)
	}

	out, err := runCLI(t, "-r", "-e", `s/^(Foo|Bar)$/same/`, root)
	if err == nil {
		t.Fatalf("conflicting recursive run unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(out, "target exists") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(root, "Bar")); statErr != nil {
		t.Fatalf("Stat(Bar): %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, "same")); statErr != nil {
		t.Fatalf("Stat(same): %v", statErr)
	}
}

func TestCLIRejectsRemovedContinueOnError(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("go", "run", ".", "--continue-on-error", ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("removed flag unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(string(out), "flag provided but not defined") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestCLIBundledShortFlags(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "Foo Bar.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cmd := exec.Command("go", "run", ".", "--dry-run", "-lu", target)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bundled short flags failed: %v\n%s", err, out)
	}
	text := string(out)
	if !strings.Contains(text, "foo_bar.txt") {
		t.Fatalf("bundled short flags output missing transformed name:\n%s", text)
	}
}

func TestCLIDryRunReportsSummary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "Foo Bar.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out, err := runCLI(t, "--dry-run", "-lu", target)
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "\n\n[summary] planned: 1, skipped: 0, errors: 0\n") {
		t.Fatalf("dry-run output missing blank line before summary:\n%s", out)
	}
	if !strings.Contains(out, "[summary] planned: 1, skipped: 0, errors: 0") {
		t.Fatalf("dry-run output missing summary:\n%s", out)
	}
}

func TestCLIRefusesConflictingPlanBeforeRename(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	foo := filepath.Join(dir, "Foo")
	bar := filepath.Join(dir, "Bar")
	if err := os.WriteFile(foo, []byte("foo"), 0o644); err != nil {
		t.Fatalf("WriteFile foo: %v", err)
	}
	if err := os.WriteFile(bar, []byte("bar"), 0o644); err != nil {
		t.Fatalf("WriteFile bar: %v", err)
	}

	out, err := runCLI(t, "-e", "s/.*/same/", foo, bar)
	if err == nil {
		t.Fatalf("conflicting plan unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(out, "target exists") {
		t.Fatalf("conflicting plan output = %s", out)
	}
	if _, statErr := os.Stat(foo); statErr != nil {
		t.Fatalf("Stat(foo): %v", statErr)
	}
	if _, statErr := os.Stat(bar); statErr != nil {
		t.Fatalf("Stat(bar): %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "same")); !os.IsNotExist(statErr) {
		t.Fatalf("unexpected target created: %v", statErr)
	}
}

func TestCLIRecursiveFilesOnlyLeavesDirectoriesAlone(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	oldDir := filepath.Join(root, "UPPER DIR")
	oldFile := filepath.Join(oldDir, "IMG 2024.jpeg")
	if err := os.Mkdir(oldDir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(oldFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out, err := runCLI(t, "--dry-run", "-rfu", oldDir)
	if err != nil {
		t.Fatalf("recursive dry-run failed: %v\n%s", err, out)
	}
	if strings.Contains(out, root+"/upper_dir") {
		t.Fatalf("directory should not be renamed in files-only mode:\n%s", out)
	}
	if !strings.Contains(out, filepath.Join(oldDir, "IMG 2024.jpeg")+" -> "+filepath.Join(oldDir, "IMG_2024.jpeg")) {
		t.Fatalf("expected file rename plan:\n%s", out)
	}
}

func TestCollectRenamePlansFailsBeforeStarting(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Foo"), []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile old: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Bar"), []byte("other"), 0o644); err != nil {
		t.Fatalf("WriteFile other: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "same"), []byte("target"), 0o644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	r, err := parseExpression(`s/^(Foo|Bar)$/same/`)
	if err != nil {
		t.Fatalf("parseExpression: %v", err)
	}

	paths, err := collectPaths([]string{root}, true)
	if err != nil {
		t.Fatalf("collectPaths: %v", err)
	}
	plans, summary, err := collectRenamePlans(paths, options{
		transforms: []transformStep{{kind: transformRegex, replacer: r}},
	}, nil)
	if err == nil {
		t.Fatal("collectRenamePlans succeeded, want error")
	}
	if len(plans) != 0 {
		t.Fatalf("collectRenamePlans plans = %v, want none", plans)
	}
	if summary.renamed != 0 || summary.planned != 0 {
		t.Fatalf("collectRenamePlans summary = %+v", summary)
	}
	if _, err := os.Stat(filepath.Join(root, "Bar")); err != nil {
		t.Fatalf("Stat(Bar): %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Foo")); err != nil {
		t.Fatalf("Stat(Foo): %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "same")); err != nil {
		t.Fatalf("Stat(same): %v", err)
	}
}
