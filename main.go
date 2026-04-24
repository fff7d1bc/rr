package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	colorModeAuto   = "auto"
	colorModeAlways = "always"
	colorModeNever  = "never"

	ansiReset = "\x1b[0m"
	ansiOld   = "\x1b[38;5;196m"
	ansiNew   = "\x1b[38;5;46m"
)

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ", ")
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type replacer struct {
	re     *regexp.Regexp
	repl   string
	global bool
}

type transformKind int

const (
	transformRegex transformKind = iota
	transformUnderscores
	transformLower
	transformNumbering
)

type transformStep struct {
	kind      transformKind
	replacer  replacer
	numbering *numberingOptions
}

type numberingOptions struct {
	width     int
	next      int
	separator string
}

type numberingState struct {
	next int
}

type numberPrefixValue struct {
	opts *numberingOptions
}

func (n *numberPrefixValue) String() string {
	if n == nil || n.opts == nil {
		return ""
	}
	return fmt.Sprintf("%0*d%s", n.opts.width, n.opts.next, n.opts.separator)
}

func (n *numberPrefixValue) Set(value string) error {
	opts, err := parseNumbering(value)
	if err != nil {
		return err
	}
	n.opts = opts
	return nil
}

func (r replacer) Apply(input string) string {
	if r.global {
		return r.re.ReplaceAllString(input, r.repl)
	}

	idx := r.re.FindStringSubmatchIndex(input)
	if idx == nil {
		return input
	}

	var out strings.Builder
	out.WriteString(input[:idx[0]])
	out.Write(r.re.ExpandString(nil, r.repl, input, idx))
	out.WriteString(input[idx[1]:])
	return out.String()
}

func usage() {
	fmt.Fprint(os.Stderr, shortUsage())
}

func shortUsage() string {
	return `rr - batch rename files and directories

Apply a sequence of transforms to each basename:
regex substitutions, underscore cleanup, case changes,
then rename the path on disk.

Options:

  --sub, -e <expr>               Regex substitution, e.g. 's/foo/bar/g'. Repeatable.
  --underscores, -u              Replace whitespace runs with underscores.
  --lower, -l                    Convert names to lower case.
  --files-only, -f               Rename files only.
  --dirs-only, -d                Rename directories only.
  --number-prefix, -N <prefix>   Prefix each renamed item with an incrementing counter.
                                 The leading digits define width and starting value.
                                 Any remaining text becomes the separator.
                                 Example: -N 001_ -> 001_, 002_, 003_ ...
  --interactive, -i              Open an editor with planned renames before applying them.
  --color <mode>                 Colorize rename output: auto, always, never.
  --no-color                     Disable color output.
  --recursive, -r                Walk directories recursively.
  --dry-run, -n                  Print planned renames without changing anything.
  --help, -h                     Show concise help.
  --help-long                    Show extended help with examples.

Notes:

  Short boolean flags can be bundled, so -lrf means -l -r -f.
  Long flags must use --long-form, not -long-form.
  All changes are validated before any rename is applied.
  In auto mode, colors are used only on terminals that appear to support 256 colors.
  Colored output highlights the changed part of the basename in red -> green.
`
}

func longUsage() string {
	return shortUsage() + `

Examples:

  Preview changes first:
      rr -n -l -u ~/music/*

  Add a prefix to every basename:
      rr -e 's/^/archive_/' *

  Add a suffix before the extension:
      rr -e 's/(\.[^.]+)$/_edited\1/' *.jpg

  Number files by moving a capture into a cleaner name:
      rr -e 's/^IMG_([0-9]+)/photo-\1/' *.jpg

  Prefix files with 3-digit sequence numbers:
      rr -f -N 001_ *.jpg

  Prefix files with 2-digit sequence numbers and no separator:
      rr -f -N 01 *

  Lowercase names and normalize whitespace:
      rr -l -u ~/Downloads/*

  Rename only files under a tree:
      rr -rf -e 's/^IMG_//' -e 's/\.[Jj][Pp][Ee]?[Gg]$/.jpg/' ~/photos

  Clean messy whitespace and separators:
      rr -r -u -e 's/__+/_/g' ./incoming

  Remove years from names:
      rr -r -e 's/[ _-]?\([0-9]{4}\)//g' ./archive

  Remove bracketed tags like "[draft]" or "[1080p]":
      rr -r -e 's/ *\[[^]]+\]//g' ./incoming

  Swap "Lastname, Firstname" into "Firstname Lastname":
      rr -e 's/^([^,]+), (.+)$/\2 \1/' *

  Rename directories only:
      rr -rd -l './Project Folders'
`
}

func main() {
	var (
		recursive     bool
		dryRun        bool
		noColor       bool
		interactive   bool
		filesOnly     bool
		dirsOnly      bool
		colorMode     string
		help          bool
		helpLong      bool
		numberingFlag numberPrefixValue
	)

	flag.Usage = usage
	flag.Bool("lower", false, "")
	flag.Bool("l", false, "")
	flag.Bool("underscores", false, "")
	flag.Bool("u", false, "")
	flag.BoolVar(&recursive, "recursive", false, "")
	flag.BoolVar(&recursive, "r", false, "")
	flag.BoolVar(&filesOnly, "files-only", false, "")
	flag.BoolVar(&filesOnly, "f", false, "")
	flag.BoolVar(&dirsOnly, "dirs-only", false, "")
	flag.BoolVar(&dirsOnly, "d", false, "")
	flag.Var(&numberingFlag, "number-prefix", "")
	flag.Var(&numberingFlag, "N", "")
	flag.BoolVar(&interactive, "interactive", false, "")
	flag.BoolVar(&interactive, "i", false, "")
	flag.StringVar(&colorMode, "color", colorModeAuto, "")
	flag.BoolVar(&noColor, "no-color", false, "")
	flag.Var(&stringList{}, "sub", "")
	flag.Var(&stringList{}, "e", "")
	flag.BoolVar(&dryRun, "dry-run", false, "")
	flag.BoolVar(&dryRun, "n", false, "")
	flag.BoolVar(&help, "help", false, "")
	flag.BoolVar(&help, "h", false, "")
	flag.BoolVar(&helpLong, "help-long", false, "")
	parsedArgs, err := normalizeArgs(os.Args[1:])
	if err != nil {
		exitErr(err)
	}
	if err := flag.CommandLine.Parse(parsedArgs); err != nil {
		exitErr(err)
	}

	if helpLong {
		fmt.Fprint(os.Stderr, longUsage())
		os.Exit(0)
	}
	if help || flag.NArg() == 0 {
		fmt.Fprint(os.Stderr, shortUsage())
		if help {
			os.Exit(0)
		}
		os.Exit(1)
	}
	if noColor {
		colorMode = colorModeNever
	}
	if !isValidColorMode(colorMode) {
		exitErr(fmt.Errorf("invalid --color value %q; expected auto, always, or never", colorMode))
	}

	transforms, numberingOpt, err := collectTransforms(parsedArgs)
	if err != nil {
		exitErr(err)
	}
	if numberingFlag.opts != nil && numberingOpt == nil {
		numberingOpt = numberingFlag.opts
	}
	if err := validateFlagCombination(filesOnly, dirsOnly, recursive, numberingOpt); err != nil {
		exitErr(err)
	}

	opts := options{
		transforms:   transforms,
		filesOnly:    filesOnly,
		dirsOnly:     dirsOnly,
		numbering:    numberingOpt,
		colorEnabled: shouldUseColor(colorMode),
		dryRun:       dryRun,
	}

	var numbering *numberingState
	if opts.numbering != nil {
		numbering = &numberingState{next: opts.numbering.next}
	}

	var total summary
	if interactive {
		plans, baseSummary, err := collectInteractivePlans(flag.Args(), recursive, opts)
		if err != nil {
			exitErr(err)
		}
		total.add(baseSummary)
		edited, editSummary, err := runInteractive(plans, opts)
		if err != nil {
			exitErr(err)
		}
		total.add(editSummary)
		current, err := executeRenamePlans(edited, opts)
		total.add(current)
		if err != nil {
			exitErr(err)
		}
		if total.planned > 0 {
			printSummary(total, true)
		} else if total.renamed > 0 {
			printSummary(total, false)
		}
		if total.errors > 0 {
			os.Exit(1)
		}
		return
	}

	paths, err := collectPaths(flag.Args(), recursive)
	if err != nil {
		exitErr(err)
	}
	plans, baseSummary, err := collectRenamePlans(paths, opts, numbering)
	total.add(baseSummary)
	if err != nil {
		exitErr(err)
	}
	current, err := executeRenamePlans(plans, opts)
	total.add(current)
	if err != nil {
		exitErr(err)
	}

	if total.errors > 0 || total.planned > 0 {
		printSummary(total, dryRun)
	}
	if total.errors > 0 {
		os.Exit(1)
	}
}

type options struct {
	transforms   []transformStep
	filesOnly    bool
	dirsOnly     bool
	numbering    *numberingOptions
	colorEnabled bool
	dryRun       bool
}

type summary struct {
	planned int
	renamed int
	skipped int
	errors  int
}

func (s *summary) add(other summary) {
	s.planned += other.planned
	s.renamed += other.renamed
	s.skipped += other.skipped
	s.errors += other.errors
}

type renamePlan struct {
	oldPath string
	newPath string
}

func applyTransforms(name string, opts options, number int) string {
	out := name
	// Transform order is user-visible: flags run left-to-right as passed on the CLI.
	for _, step := range opts.transforms {
		switch step.kind {
		case transformRegex:
			out = step.replacer.Apply(out)
		case transformUnderscores:
			out = replaceWhitespaceRuns(out, "_")
		case transformLower:
			out = strings.ToLower(out)
		case transformNumbering:
			out = fmt.Sprintf("%0*d%s%s", step.numbering.width, number, step.numbering.separator, out)
		}
	}
	return out
}

func renamePath(oldPath string, opts options) (summary, error) {
	if oldPath == "." {
		return summary{skipped: 1}, nil
	}
	info, err := os.Lstat(oldPath)
	if err != nil {
		return summary{errors: 1}, formatError("%s: %w", oldPath, err)
	}
	if opts.filesOnly && info.IsDir() {
		return summary{skipped: 1}, nil
	}
	if opts.dirsOnly && !info.IsDir() {
		return summary{skipped: 1}, nil
	}

	base := filepath.Base(oldPath)
	dir := filepath.Dir(oldPath)
	number := 0
	if opts.numbering != nil {
		number = opts.numbering.next
	}
	newName := applyTransforms(base, opts, number)
	if newName == base {
		return summary{skipped: 1}, nil
	}
	if newName == "" {
		return summary{errors: 1}, formatError("%s: transformation produced an empty name", oldPath)
	}

	newPath := filepath.Join(dir, newName)
	if conflict, err := targetPathConflicts(oldPath, info, newPath); err != nil {
		return summary{errors: 1}, formatError("%s: %w", newPath, err)
	} else if conflict {
		return summary{errors: 1}, formatError("target exists: %s", newPath)
	}

	result, err := applyRenamePlan(renamePlan{oldPath: oldPath, newPath: newPath}, opts)
	if err == nil && opts.numbering != nil {
		opts.numbering.next++
	}
	return result, err
}

func tempRenamePath(dir, newName string) (string, error) {
	for i := 0; i < 1000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf(".%s.rrtmp.%d", newName, i))
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", formatError("%s: %w", candidate, err)
		}
	}

	return "", formatError("could not allocate temporary path for %s", filepath.Join(dir, newName))
}

func buildNumberedPlans(paths []string, opts options, numbering *numberingState) ([]renamePlan, int, error) {
	if numbering == nil || opts.numbering == nil {
		return nil, 0, errors.New("numbering preflight requires numbering options")
	}

	next := numbering.next
	var plans []renamePlan
	skipped := 0
	seenTargets := make(map[string]struct{})

	for _, oldPath := range paths {
		// Numbered runs are preflighted as a batch so we either reserve a full safe plan
		// up front or leave the numbering state untouched.
		plan, changed, err := planRename(oldPath, opts, next)
		if err != nil {
			return nil, skipped, err
		}
		if !changed {
			skipped++
			continue
		}
		next++
		if _, exists := seenTargets[plan.newPath]; exists {
			return nil, skipped, formatError("target exists: %s", plan.newPath)
		}
		seenTargets[plan.newPath] = struct{}{}
		plans = append(plans, plan)
	}

	numbering.next = next
	return plans, skipped, nil
}

func planRename(oldPath string, opts options, number int) (renamePlan, bool, error) {
	if oldPath == "." {
		return renamePlan{}, false, nil
	}
	info, err := os.Lstat(oldPath)
	if err != nil {
		return renamePlan{}, false, formatError("%s: %w", oldPath, err)
	}
	if opts.filesOnly && info.IsDir() {
		return renamePlan{}, false, nil
	}
	if opts.dirsOnly && !info.IsDir() {
		return renamePlan{}, false, nil
	}

	base := filepath.Base(oldPath)
	dir := filepath.Dir(oldPath)
	newName := applyTransforms(base, opts, number)
	if newName == base {
		return renamePlan{}, false, nil
	}
	if newName == "" {
		return renamePlan{}, false, formatError("%s: transformation produced an empty name", oldPath)
	}

	newPath := filepath.Join(dir, newName)
	if conflict, err := targetPathConflicts(oldPath, info, newPath); err != nil {
		return renamePlan{}, false, formatError("%s: %w", newPath, err)
	} else if conflict {
		return renamePlan{}, false, formatError("target exists: %s", newPath)
	}

	return renamePlan{oldPath: oldPath, newPath: newPath}, true, nil
}

func planInteractiveRename(oldPath string, opts options, number int) (renamePlan, bool, error) {
	if oldPath == "." {
		return renamePlan{}, false, nil
	}
	info, err := os.Lstat(oldPath)
	if err != nil {
		return renamePlan{}, false, formatError("%s: %w", oldPath, err)
	}
	if opts.filesOnly && info.IsDir() {
		return renamePlan{}, false, nil
	}
	if opts.dirsOnly && !info.IsDir() {
		return renamePlan{}, false, nil
	}

	base := filepath.Base(oldPath)
	dir := filepath.Dir(oldPath)
	newName := applyTransforms(base, opts, number)

	return renamePlan{oldPath: oldPath, newPath: filepath.Join(dir, newName)}, true, nil
}

func collectPaths(args []string, recursive bool) ([]string, error) {
	var paths []string
	for _, arg := range args {
		if recursive {
			var current []string
			err := filepath.WalkDir(arg, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				current = append(current, path)
				return nil
			})
			if err != nil {
				return nil, err
			}
			// Reverse post-order so children are planned before parents.
			for i := len(current) - 1; i >= 0; i-- {
				paths = append(paths, current[i])
			}
			continue
		}
		paths = append(paths, arg)
	}
	return paths, nil
}

func collectInteractivePlans(args []string, recursive bool, opts options) ([]renamePlan, summary, error) {
	paths, err := collectPaths(args, recursive)
	if err != nil {
		return nil, summary{}, err
	}
	return collectInteractivePlansForPaths(paths, opts)
}

func runInteractive(plans []renamePlan, opts options) ([]renamePlan, summary, error) {
	current := plans
	reader := bufio.NewReader(os.Stdin)

	for {
		edited, err := editPlans(current)
		if err != nil {
			return nil, summary{}, err
		}
		validated, skipped, err := validateEditedPlans(edited)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		for _, plan := range validated {
			fmt.Println(formatRename(plan.oldPath, plan.newPath, opts.colorEnabled, true))
		}
		if len(validated) == 0 {
			fmt.Fprintln(os.Stderr, "plan: no changes")
		}
		fmt.Fprint(os.Stderr, "Proceed? [y]es/[e]dit/[c]ancel: ")
		answer, err := reader.ReadString('\n')
		if err != nil {
			return nil, summary{}, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes":
			return validated, summary{skipped: skipped}, nil
		case "e", "edit":
			current = edited
		case "c", "cancel":
			return nil, summary{}, errors.New("canceled")
		default:
			fmt.Fprintln(os.Stderr, "error: expected yes, edit, or cancel")
			current = edited
		}
	}
}

func editPlans(plans []renamePlan) ([]renamePlan, error) {
	f, err := os.CreateTemp("", "rr-plan-*.txt")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	defer os.Remove(path)

	if _, err := fmt.Fprintln(f, "# Edit only the second column. Format: <old><TAB><new>"); err != nil {
		_ = f.Close()
		return nil, err
	}
	for _, plan := range plans {
		if _, err := fmt.Fprintf(f, "%q\t%q\n", plan.oldPath, plan.newPath); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	if err := f.Close(); err != nil {
		return nil, err
	}

	cmd := exec.Command("sh", "-c", `${EDITOR:-vi} "$1"`, "rr", path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	return parseEditedPlans(path, plans)
}

func parseEditedPlans(path string, original []renamePlan) ([]renamePlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var edited []renamePlan
	lines := strings.Split(string(data), "\n")
	index := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if index >= len(original) {
			return nil, formatError("interactive plan has more entries than expected")
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			return nil, formatError("invalid interactive line %q", line)
		}
		oldPath, err := strconv.Unquote(parts[0])
		if err != nil {
			return nil, formatError("invalid old path %q", parts[0])
		}
		newPath, err := strconv.Unquote(parts[1])
		if err != nil {
			return nil, formatError("invalid new path %q", parts[1])
		}
		if oldPath != original[index].oldPath {
			return nil, formatError("interactive plan changed or reordered old paths")
		}
		edited = append(edited, renamePlan{oldPath: oldPath, newPath: newPath})
		index++
	}
	if index != len(original) {
		return nil, formatError("interactive plan has fewer entries than expected")
	}
	return edited, nil
}

func validateEditedPlans(plans []renamePlan) ([]renamePlan, int, error) {
	var out []renamePlan
	skipped := 0
	movingOldPaths := make(map[string]struct{}, len(plans))
	seenOldPaths := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		if _, exists := seenOldPaths[plan.oldPath]; exists {
			return nil, skipped, formatError("duplicate source path: %s", plan.oldPath)
		}
		seenOldPaths[plan.oldPath] = struct{}{}
		if plan.newPath != "" && plan.newPath != plan.oldPath {
			movingOldPaths[plan.oldPath] = struct{}{}
		}
	}

	seenTargets := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		if plan.newPath == "" {
			return nil, skipped, formatError("interactive plan produced an empty target for %s", plan.oldPath)
		}
		if plan.newPath == plan.oldPath {
			skipped++
			continue
		}
		if _, exists := seenTargets[plan.newPath]; exists {
			return nil, skipped, formatError("target exists: %s", plan.newPath)
		}
		seenTargets[plan.newPath] = struct{}{}
		// Only targets that will actually move away are safe to reuse in the edited plan.
		if _, plannedSource := movingOldPaths[plan.newPath]; plannedSource {
			out = append(out, plan)
			continue
		}
		if conflict, err := targetPathConflicts(plan.oldPath, nil, plan.newPath); err != nil {
			return nil, skipped, formatError("%s: %w", plan.newPath, err)
		} else if conflict {
			return nil, skipped, formatError("target exists: %s", plan.newPath)
		}
		out = append(out, plan)
	}

	return out, skipped, nil
}

func applyRenamePlan(plan renamePlan, opts options) (summary, error) {
	if opts.dryRun {
		fmt.Println(formatRename(plan.oldPath, plan.newPath, opts.colorEnabled, true))
		return summary{planned: 1}, nil
	}

	tmpPath, err := tempRenamePath(filepath.Dir(plan.oldPath), filepath.Base(plan.newPath))
	if err != nil {
		return summary{errors: 1}, err
	}

	if err := os.Rename(plan.oldPath, tmpPath); err != nil {
		return summary{errors: 1}, formatError("rename %s -> %s: %w", plan.oldPath, tmpPath, err)
	}
	if err := os.Rename(tmpPath, plan.newPath); err != nil {
		_ = os.Rename(tmpPath, plan.oldPath)
		return summary{errors: 1}, formatError("rename %s -> %s: %w", tmpPath, plan.newPath, err)
	}

	fmt.Println(formatRename(plan.oldPath, plan.newPath, opts.colorEnabled, false))
	return summary{renamed: 1}, nil
}

func collectRenamePlans(paths []string, opts options, numbering *numberingState) ([]renamePlan, summary, error) {
	if opts.numbering != nil {
		plans, skipped, err := buildNumberedPlans(paths, opts, numbering)
		return plans, summary{skipped: skipped}, err
	}

	number := 0
	var plans []renamePlan
	var total summary
	for _, path := range paths {
		plan, changed, err := planRename(path, opts, number)
		if err != nil {
			return nil, total, err
		}
		if !changed {
			total.skipped++
			continue
		}
		plans = append(plans, plan)
	}
	if err := validateRenamePlans(plans); err != nil {
		return nil, total, err
	}
	return plans, total, nil
}

func collectInteractivePlansForPaths(paths []string, opts options) ([]renamePlan, summary, error) {
	number := 0
	if opts.numbering != nil {
		number = opts.numbering.next
	}

	var plans []renamePlan
	var total summary
	for _, path := range paths {
		plan, include, err := planInteractiveRename(path, opts, number)
		if err != nil {
			return nil, total, err
		}
		if !include {
			total.skipped++
			continue
		}
		if opts.numbering != nil {
			number++
		}
		plans = append(plans, plan)
	}
	return plans, total, nil
}

func validateRenamePlans(plans []renamePlan) error {
	oldPaths := make(map[string]struct{}, len(plans))
	targets := make(map[string]struct{}, len(plans))

	for _, plan := range plans {
		if _, exists := oldPaths[plan.oldPath]; exists {
			return formatError("duplicate source path: %s", plan.oldPath)
		}
		oldPaths[plan.oldPath] = struct{}{}
	}

	for _, plan := range plans {
		if _, exists := targets[plan.newPath]; exists {
			return formatError("target exists: %s", plan.newPath)
		}
		targets[plan.newPath] = struct{}{}
		if _, plannedSource := oldPaths[plan.newPath]; plannedSource {
			continue
		}
		if conflict, err := targetPathConflicts(plan.oldPath, nil, plan.newPath); err != nil {
			return formatError("%s: %w", plan.newPath, err)
		} else if conflict {
			return formatError("target exists: %s", plan.newPath)
		}
	}

	return nil
}

func executeRenamePlans(plans []renamePlan, opts options) (summary, error) {
	if opts.dryRun {
		var total summary
		for _, plan := range plans {
			current, err := applyRenamePlan(plan, opts)
			total.add(current)
			if err != nil {
				return total, err
			}
		}
		return total, nil
	}

	type pendingRename struct {
		currentPath string
		finalPath   string
		displayOld  string
	}

	pending := make([]*pendingRename, 0, len(plans))
	currentPaths := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		pending = append(pending, &pendingRename{
			currentPath: plan.oldPath,
			finalPath:   plan.newPath,
			displayOld:  plan.oldPath,
		})
		currentPaths[plan.oldPath] = struct{}{}
	}

	var total summary
	for len(pending) > 0 {
		progressed := false
		nextPending := pending[:0]
		for _, plan := range pending {
			// A rename can proceed once no other pending item still occupies its destination.
			if _, blocked := currentPaths[plan.finalPath]; blocked {
				nextPending = append(nextPending, plan)
				continue
			}
			if err := os.Rename(plan.currentPath, plan.finalPath); err != nil {
				return total, formatError("rename %s -> %s: %w", plan.currentPath, plan.finalPath, err)
			}
			fmt.Println(formatRename(plan.displayOld, plan.finalPath, opts.colorEnabled, false))
			total.renamed++
			delete(currentPaths, plan.currentPath)
			progressed = true
		}
		pending = nextPending
		if progressed {
			continue
		}

		// No destination is currently free, so we have a cycle such as a<->b.
		// Break it by moving one element to a temporary sibling path first.
		cycle := pending[0]
		tmpPath, err := tempRenamePath(filepath.Dir(cycle.currentPath), filepath.Base(cycle.finalPath))
		if err != nil {
			return total, err
		}
		if err := os.Rename(cycle.currentPath, tmpPath); err != nil {
			return total, formatError("rename %s -> %s: %w", cycle.currentPath, tmpPath, err)
		}
		delete(currentPaths, cycle.currentPath)
		cycle.currentPath = tmpPath
		currentPaths[tmpPath] = struct{}{}
	}

	return total, nil
}

const (
	shortValueFlags   = "Ne"
	shortBooleanFlags = "dfhilnru"
)

func isShortFlagAllowed(r rune) bool {
	return strings.ContainsRune(shortValueFlags, r) || strings.ContainsRune(shortBooleanFlags, r)
}

func parseNumbering(value string) (*numberingOptions, error) {
	if value == "" {
		return nil, errors.New("invalid number prefix \"\"; expected digits with optional separator, e.g. 001_")
	}

	i := 0
	for i < len(value) && value[i] >= '0' && value[i] <= '9' {
		i++
	}
	if i == 0 {
		return nil, fmt.Errorf("invalid number prefix %q; expected digits with optional separator, e.g. 001_", value)
	}

	start, err := strconv.Atoi(value[:i])
	if err != nil {
		return nil, fmt.Errorf("invalid number prefix %q: %w", value, err)
	}

	return &numberingOptions{
		width:     i,
		next:      start,
		separator: value[i:],
	}, nil
}

func validateFlagCombination(filesOnly, dirsOnly, recursive bool, numbering *numberingOptions) error {
	if filesOnly && dirsOnly {
		return errors.New("cannot combine --files-only and --dirs-only")
	}
	if recursive && numbering != nil {
		return errors.New("cannot combine --recursive with --number-prefix")
	}
	return nil
}

func collectTransforms(args []string) ([]transformStep, *numberingOptions, error) {
	var transforms []transformStep
	var numbering *numberingOptions

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}

		switch {
		case arg == "-l" || arg == "--lower":
			transforms = append(transforms, transformStep{kind: transformLower})
		case arg == "-u" || arg == "--underscores":
			transforms = append(transforms, transformStep{kind: transformUnderscores})
		case arg == "-e" || arg == "--sub":
			if i+1 >= len(args) {
				return nil, nil, errors.New("missing value for --sub")
			}
			i++
			r, err := parseExpression(args[i])
			if err != nil {
				return nil, nil, err
			}
			transforms = append(transforms, transformStep{kind: transformRegex, replacer: r})
		case strings.HasPrefix(arg, "--sub="):
			r, err := parseExpression(strings.TrimPrefix(arg, "--sub="))
			if err != nil {
				return nil, nil, err
			}
			transforms = append(transforms, transformStep{kind: transformRegex, replacer: r})
		case strings.HasPrefix(arg, "-e="):
			r, err := parseExpression(strings.TrimPrefix(arg, "-e="))
			if err != nil {
				return nil, nil, err
			}
			transforms = append(transforms, transformStep{kind: transformRegex, replacer: r})
		case arg == "-N" || arg == "--number-prefix":
			if numbering != nil {
				return nil, nil, errors.New("cannot specify --number-prefix more than once")
			}
			if i+1 >= len(args) {
				return nil, nil, errors.New("missing value for --number-prefix")
			}
			i++
			n, err := parseNumbering(args[i])
			if err != nil {
				return nil, nil, err
			}
			numbering = n
			transforms = append(transforms, transformStep{kind: transformNumbering, numbering: n})
		case strings.HasPrefix(arg, "--number-prefix="):
			if numbering != nil {
				return nil, nil, errors.New("cannot specify --number-prefix more than once")
			}
			n, err := parseNumbering(strings.TrimPrefix(arg, "--number-prefix="))
			if err != nil {
				return nil, nil, err
			}
			numbering = n
			transforms = append(transforms, transformStep{kind: transformNumbering, numbering: n})
		case strings.HasPrefix(arg, "-N="):
			if numbering != nil {
				return nil, nil, errors.New("cannot specify --number-prefix more than once")
			}
			n, err := parseNumbering(strings.TrimPrefix(arg, "-N="))
			if err != nil {
				return nil, nil, err
			}
			numbering = n
			transforms = append(transforms, transformStep{kind: transformNumbering, numbering: n})
		}
	}

	// Parsing transforms separately from the flag package lets us preserve CLI order.
	return transforms, numbering, nil
}

func parseExpression(expr string) (replacer, error) {
	if len(expr) < 4 || expr[0] != 's' {
		return replacer{}, fmt.Errorf("unsupported expression %q; expected s<delim>pattern<delim>replacement<delim>flags", expr)
	}

	delim := rune(expr[1])
	pattern, next, err := readSection(expr, 2, delim)
	if err != nil {
		return replacer{}, err
	}
	replacement, next, err := readSection(expr, next, delim)
	if err != nil {
		return replacer{}, err
	}

	flags := expr[next:]
	global := false
	caseInsensitive := false
	for _, flag := range flags {
		switch flag {
		case 'g':
			global = true
		case 'i':
			caseInsensitive = true
		case 0:
		default:
			return replacer{}, fmt.Errorf("unsupported expression flag %q in %q", string(flag), expr)
		}
	}

	compiledPattern := pattern
	if caseInsensitive {
		compiledPattern = "(?i)" + compiledPattern
	}

	re, err := regexp.Compile(compiledPattern)
	if err != nil {
		return replacer{}, fmt.Errorf("compile %q: %w", expr, err)
	}

	return replacer{
		re:     re,
		repl:   normalizeReplacement(replacement),
		global: global,
	}, nil
}

func readSection(expr string, start int, delim rune) (string, int, error) {
	var section strings.Builder

	for i := start; i < len(expr); i++ {
		ch := rune(expr[i])
		if ch == delim {
			return section.String(), i + 1, nil
		}
		if ch == '\\' && i+1 < len(expr) {
			next := rune(expr[i+1])
			if next == delim {
				// Support sed-style escaped delimiters without interpreting other escapes.
				section.WriteRune(delim)
				i++
				continue
			}
			section.WriteByte(expr[i])
			section.WriteByte(expr[i+1])
			i++
			continue
		}
		section.WriteByte(expr[i])
	}

	return "", 0, fmt.Errorf("unterminated expression %q", expr)
}

func normalizeReplacement(in string) string {
	var out strings.Builder

	for i := 0; i < len(in); i++ {
		if in[i] == '\\' && i+1 < len(in) && in[i+1] >= '0' && in[i+1] <= '9' {
			out.WriteByte('$')
			i++
			for ; i < len(in) && in[i] >= '0' && in[i] <= '9'; i++ {
				out.WriteByte(in[i])
			}
			i--
			continue
		}
		out.WriteByte(in[i])
	}

	return out.String()
}

func replaceWhitespaceRuns(input, replacement string) string {
	var out strings.Builder
	inWhitespace := false

	for _, r := range input {
		if unicode.IsSpace(r) {
			if !inWhitespace {
				out.WriteString(replacement)
				inWhitespace = true
			}
			continue
		}

		inWhitespace = false
		out.WriteRune(r)
	}

	return out.String()
}

func formatRename(oldPath, newPath string, colorEnabled, dryRun bool) string {
	verb := "rename"
	if dryRun {
		verb = "plan"
	}
	prefix := fmt.Sprintf("[%s] ", verb)
	if !colorEnabled {
		return fmt.Sprintf("%s%s -> %s", prefix, oldPath, newPath)
	}

	oldDir, oldBase := filepath.Split(oldPath)
	newDir, newBase := filepath.Split(newPath)
	if oldDir != newDir {
		return fmt.Sprintf("%s%s -> %s", prefix, oldPath, newPath)
	}

	oldPrefix, oldChanged, oldSuffix, newPrefix, newChanged, newSuffix := diffStrings(oldBase, newBase)
	if oldChanged == "" && newChanged == "" {
		return fmt.Sprintf("%s%s -> %s", prefix, oldPath, newPath)
	}

	return fmt.Sprintf(
		"%s%s%s%s%s -> %s%s%s%s",
		prefix,
		oldDir,
		oldPrefix,
		colorize(oldChanged, ansiOld),
		oldSuffix,
		newDir+newPrefix,
		colorize(newChanged, ansiNew),
		newSuffix,
		"",
	)
}

func diffStrings(oldText, newText string) (string, string, string, string, string, string) {
	oldRunes := []rune(oldText)
	newRunes := []rune(newText)

	prefixLen := 0
	for prefixLen < len(oldRunes) && prefixLen < len(newRunes) && oldRunes[prefixLen] == newRunes[prefixLen] {
		prefixLen++
	}

	oldSuffixLen := len(oldRunes)
	newSuffixLen := len(newRunes)
	for oldSuffixLen > prefixLen && newSuffixLen > prefixLen && oldRunes[oldSuffixLen-1] == newRunes[newSuffixLen-1] {
		oldSuffixLen--
		newSuffixLen--
	}

	return string(oldRunes[:prefixLen]),
		string(oldRunes[prefixLen:oldSuffixLen]),
		string(oldRunes[oldSuffixLen:]),
		string(newRunes[:prefixLen]),
		string(newRunes[prefixLen:newSuffixLen]),
		string(newRunes[newSuffixLen:])
}

func colorize(text, color string) string {
	if text == "" {
		return ""
	}
	return color + text + ansiReset
}

func isValidColorMode(mode string) bool {
	switch mode {
	case colorModeAuto, colorModeAlways, colorModeNever:
		return true
	default:
		return false
	}
}

func shouldUseColor(mode string) bool {
	info, err := os.Stdout.Stat()
	isTTY := err == nil && (info.Mode()&os.ModeCharDevice) != 0
	return shouldUseColorEnv(mode, isTTY, os.Getenv)
}

func shouldUseColorEnv(mode string, isTTY bool, getenv func(string) string) bool {
	switch mode {
	case colorModeAlways:
		return true
	case colorModeNever:
		return false
	case colorModeAuto:
		if getenv("NO_COLOR") != "" {
			return false
		}
		if getenv("TERM") == "dumb" {
			return false
		}
		if !isTTY {
			return false
		}
		return supports256ColorEnv(getenv)
	default:
		return false
	}
}

func supports256Color() bool {
	return supports256ColorEnv(os.Getenv)
}

func supports256ColorEnv(getenv func(string) string) bool {
	if colorterm := strings.ToLower(getenv("COLORTERM")); strings.Contains(colorterm, "truecolor") || strings.Contains(colorterm, "24bit") {
		return true
	}
	if term := strings.ToLower(getenv("TERM")); strings.Contains(term, "256color") {
		return true
	}
	if termProgram := strings.ToLower(getenv("TERM_PROGRAM")); termProgram == "wezterm" {
		return true
	}
	if v := getenv("TERM_PROGRAM_VERSION"); v != "" && strings.ToLower(getenv("TERM_PROGRAM")) == "apple_terminal" {
		return true
	}
	if value := getenv("NO_COLOR"); value != "" {
		return false
	}
	if colors := getenv("COLORS"); colors != "" {
		n, err := strconv.Atoi(colors)
		return err == nil && n >= 256
	}
	return false
}

func targetPathConflicts(oldPath string, oldInfo fs.FileInfo, newPath string) (bool, error) {
	newInfo, err := os.Lstat(newPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if oldInfo == nil {
		oldInfo, err = os.Lstat(oldPath)
		if err != nil {
			return false, err
		}
	}

	oldDir := filepath.Clean(filepath.Dir(oldPath))
	newDir := filepath.Clean(filepath.Dir(newPath))
	oldBase := filepath.Base(oldPath)
	newBase := filepath.Base(newPath)
	if oldDir == newDir && strings.EqualFold(oldBase, newBase) && os.SameFile(oldInfo, newInfo) {
		// On case-insensitive filesystems "Foo" -> "foo" resolves to the same file.
		// Allow that, but only when the directory does not contain both spellings.
		hasBoth, err := dirHasDistinctEntries(oldDir, oldBase, newBase)
		if err != nil {
			return false, err
		}
		if !hasBoth {
			return false, nil
		}
	}
	return true, nil
}

func dirHasDistinctEntries(dir, first, second string) (bool, error) {
	if first == second {
		return false, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	hasFirst := false
	hasSecond := false
	for _, entry := range entries {
		name := entry.Name()
		if name == first {
			hasFirst = true
		}
		if name == second {
			hasSecond = true
		}
	}
	return hasFirst && hasSecond, nil
}

func normalizeArgs(args []string) ([]string, error) {
	var normalized []string

	for i, arg := range args {
		if arg == "--" {
			normalized = append(normalized, args[i:]...)
			return normalized, nil
		}
		if len(arg) == 0 || arg[0] != '-' || strings.HasPrefix(arg, "--") {
			normalized = append(normalized, arg)
			continue
		}

		name := strings.TrimPrefix(arg, "-")
		if len(name) == 0 {
			normalized = append(normalized, arg)
			continue
		}

		if idx := strings.IndexByte(name, '='); idx >= 0 {
			flagName := name[:idx]
			if utf8.RuneCountInString(flagName) != 1 {
				return nil, fmt.Errorf("invalid flag %q; use --%s for long options", arg, flagName)
			}
			normalized = append(normalized, arg)
			continue
		}

		runes := []rune(name)
		if len(runes) == 1 {
			if !isShortFlagAllowed(runes[0]) {
				return nil, fmt.Errorf("invalid flag %q", arg)
			}
			normalized = append(normalized, arg)
			continue
		}

		if strings.ContainsRune(name, '=') {
			return nil, fmt.Errorf("invalid flag %q", arg)
		}

		for _, r := range runes {
			switch {
			case strings.ContainsRune(shortBooleanFlags, r):
				// Expand bundled booleans like -lru into -l -r -u.
				normalized = append(normalized, "-"+string(r))
			case strings.ContainsRune(shortValueFlags, r):
				return nil, fmt.Errorf("invalid bundled short flag %q in %q", "-"+string(r), arg)
			default:
				return nil, fmt.Errorf("invalid flag %q; use --%s for long options", arg, name)
			}
		}
	}

	return normalized, nil
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func formatError(format string, args ...any) error {
	return fmt.Errorf("error: "+format, args...)
}

func printSummary(total summary, dryRun bool) {
	fmt.Println()
	if dryRun {
		fmt.Printf("[summary] planned: %d, skipped: %d, errors: %d\n", total.planned, total.skipped, total.errors)
		return
	}
	fmt.Printf("[summary] renamed: %d, skipped: %d, errors: %d\n", total.renamed, total.skipped, total.errors)
}
