package main

import (
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

func TestRenamePathAppliesTransforms(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "Foo Bar.JPG")
	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := renamePath(oldPath, options{
		transforms: []transformStep{lowerStep(), underscoreStep()},
	})
	if err != nil {
		t.Fatalf("renamePath: %v", err)
	}
	if result.renamed != 1 || result.planned != 0 || result.skipped != 0 || result.errors != 0 {
		t.Fatalf("renamePath summary = %+v", result)
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

func TestRenamePathRejectsExistingTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "Foo")
	newPath := filepath.Join(dir, "foo")

	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile old: %v", err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o644); err != nil {
		t.Fatalf("WriteFile new: %v", err)
	}

	result, err := renamePath(oldPath, options{transforms: []transformStep{lowerStep()}})
	if err == nil {
		t.Fatal("renamePath succeeded, want error")
	}
	if result.errors != 1 {
		t.Fatalf("renamePath summary = %+v", result)
	}
	if !strings.Contains(err.Error(), "target exists") {
		t.Fatalf("renamePath error = %v, want target exists", err)
	}
}

func TestRenamePathReplacesWhitespaceRuns(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "  Foo   Bar  .txt  ")
	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := renamePath(oldPath, options{transforms: []transformStep{underscoreStep()}})
	if err != nil {
		t.Fatalf("renamePath: %v", err)
	}
	if result.renamed != 1 {
		t.Fatalf("renamePath summary = %+v", result)
	}

	newPath := filepath.Join(dir, "_Foo_Bar_.txt_")
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("Stat(%q): %v", newPath, err)
	}
}

func TestRenamePathFilesOnlySkipsDirectories(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	targetDir := filepath.Join(dir, "Folder Name")
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	result, err := renamePath(targetDir, options{
		filesOnly:  true,
		transforms: []transformStep{underscoreStep()},
	})
	if err != nil {
		t.Fatalf("renamePath: %v", err)
	}
	if result.skipped != 1 {
		t.Fatalf("renamePath summary = %+v", result)
	}

	if _, err := os.Stat(targetDir); err != nil {
		t.Fatalf("Stat(%q): %v", targetDir, err)
	}
}

func TestRenamePathNumberPrefix(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "Photo.JPG")
	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	numbering := &numberingOptions{width: 3, next: 1, separator: "_"}
	result, err := renamePath(oldPath, options{
		transforms: []transformStep{numberingStep(3, 1, "_")},
		numbering:  numbering,
	})
	if err != nil {
		t.Fatalf("renamePath: %v", err)
	}
	if result.renamed != 1 {
		t.Fatalf("renamePath summary = %+v", result)
	}
	if numbering.next != 2 {
		t.Fatalf("numbering.next = %d, want 2", numbering.next)
	}
	if _, err := os.Stat(filepath.Join(dir, "001_Photo.JPG")); err != nil {
		t.Fatalf("Stat numbered file: %v", err)
	}
}

func TestRenamePathNumberPrefixWithoutSeparator(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "foo.txt")
	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := renamePath(oldPath, options{
		transforms: []transformStep{numberingStep(2, 1, "")},
		numbering:  &numberingOptions{width: 2, next: 1, separator: ""},
	})
	if err != nil {
		t.Fatalf("renamePath: %v", err)
	}
	if result.renamed != 1 {
		t.Fatalf("renamePath summary = %+v", result)
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

	plans, skipped, err := validateEditedPlans([]renamePlan{
		{oldPath: "/tmp/a", newPath: "/tmp/b"},
		{oldPath: "/tmp/b", newPath: "/tmp/a"},
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
	oldPath := filepath.Join(dir, "Foo")
	hardLinkPath := filepath.Join(dir, "foo")
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

func TestRenamePathNumberingDoesNotAdvanceOnError(t *testing.T) {
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
	result, err := renamePath(oldPath, options{
		transforms: []transformStep{numberingStep(3, 1, "_")},
		numbering:  numbering,
	})
	if err == nil {
		t.Fatal("renamePath succeeded, want error")
	}
	if result.errors != 1 {
		t.Fatalf("renamePath summary = %+v", result)
	}
	if numbering.next != 1 {
		t.Fatalf("numbering.next = %d, want unchanged 1", numbering.next)
	}
}

func TestApplyRenamePlanDryRunCountsPlanned(t *testing.T) {
	t.Parallel()

	result, err := applyRenamePlan(renamePlan{
		oldPath: "/tmp/old.txt",
		newPath: "/tmp/new.txt",
	}, options{dryRun: true})
	if err != nil {
		t.Fatalf("applyRenamePlan: %v", err)
	}
	if result.planned != 1 || result.renamed != 0 {
		t.Fatalf("applyRenamePlan summary = %+v", result)
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
	if err := os.WriteFile(filepath.Join(root, "foo"), []byte("new"), 0o644); err != nil {
		t.Fatalf("WriteFile foo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Bar"), []byte("other"), 0o644); err != nil {
		t.Fatalf("WriteFile Bar: %v", err)
	}

	out, err := runCLI(t, "-r", "-l", root)
	if err == nil {
		t.Fatalf("conflicting recursive run unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(out, "target exists") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(root, "Bar")); statErr != nil {
		t.Fatalf("Stat(Bar): %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, "bar")); !os.IsNotExist(statErr) {
		t.Fatalf("bar unexpectedly exists: %v", statErr)
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
	if err := os.WriteFile(filepath.Join(root, "foo"), []byte("new"), 0o644); err != nil {
		t.Fatalf("WriteFile new: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Bar"), []byte("other"), 0o644); err != nil {
		t.Fatalf("WriteFile other: %v", err)
	}

	paths, err := collectPaths([]string{root}, true)
	if err != nil {
		t.Fatalf("collectPaths: %v", err)
	}
	plans, summary, err := collectRenamePlans(paths, options{
		transforms: []transformStep{lowerStep()},
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
	if _, err := os.Stat(filepath.Join(root, "bar")); !os.IsNotExist(err) {
		t.Fatalf("bar unexpectedly exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Foo")); err != nil {
		t.Fatalf("Stat(Foo): %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "foo")); err != nil {
		t.Fatalf("Stat(foo): %v", err)
	}
}
