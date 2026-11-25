package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/config"
	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/view"
	"github.com/rojcarranza/bam/internal/view/tui"
	"github.com/spf13/cobra"
)

// presetEditFlags holds bam preset edit's flags.
type presetEditFlags struct {
	vars, unset    []string
	branch, rename string
	timeout        string
	repositories   []string
	required       string
	options        []string
	dryRun         bool
}

// editDoc is bam preset edit --json. Field names are a public contract.
type editDoc struct {
	Preset  string               `json:"preset"`
	Renamed string               `json:"renamed_to,omitempty"`
	File    string               `json:"file"`
	DryRun  bool                 `json:"dry_run"`
	Changed bool                 `json:"changed"`
	Changes []config.FieldChange `json:"changes"`
}

func newPresetEditCmd(r *runtime) *cobra.Command {
	var fl presetEditFlags
	cmd := &cobra.Command{
		Use:   "edit [name]",
		Short: "Change a saved preset: values, branch, repositories, required, options, timeout or name",
		Long: "Change a saved preset without opening its YAML. bam edits the file whose copy of the preset\n" +
			"bam uses and keeps its comments, key order and the other presets; the spacing before a comment can change.\n" +
			"On a terminal, bam preset edit [NAME] opens a form filled with the saved preset: its variables,\n" +
			"the plan variables it leaves out (written only once you change them), then name, branch,\n" +
			"repositories, required and timeout. enter edits a row, ←→ step through the choices, x removes a\n" +
			"variable or repository (a variable then takes the plan's own value), o edits a variable's options,\n" +
			"a adds a repository, ctrl-s saves and esc writes nothing. When you change the branch, the form\n" +
			"reads the repositories Bamboo lists for it and offers them in place of the old ones.\n" +
			"With --var, --unset, --branch, --repository, --required, --option, --timeout or --rename there is\n" +
			"no form: bam applies them and never asks, so scripts and agents can use it, and it reads nothing\n" +
			"from Bamboo. --dry-run shows the changes and writes nothing.\n" +
			"A rename also renames the preset in the stages of the sets and the bam top pins in your project\n" +
			"and machine files, and in each train file a past bam set run --file used (a set in another\n" +
			"repository's own .bam.yaml keeps the old name).\n" +
			"A secret's value is never written: give ${ENV_NAME} and bam reads it from your shell at run\n" +
			"time. A preset that uses YAML anchors is refused.",
		Example: "  bam preset edit\n" +
			"  bam preset edit deploy-qa\n" +
			"  bam preset edit deploy-qa --var ENV=prod --unset REGION\n" +
			"  bam preset edit deploy-qa --branch release-2 --timeout 1h --dry-run\n" +
			"  bam preset edit deploy-qa --repository app=release-2 --repository docs= --required ENV,REGION\n" +
			"  bam preset edit deploy-qa --option ENV=qa,prod\n" +
			"  bam preset edit deploy-qa --rename deploy-prod --json",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return fl.run(r, cmd, name)
		}),
	}
	f := cmd.Flags()
	f.StringArrayVar(&fl.vars, "var", nil, "set a variable, name=value (repeatable)")
	f.StringArrayVar(&fl.unset, "unset", nil, "remove a variable so the plan's own value applies (repeatable)")
	f.StringVarP(&fl.branch, "branch", "b", "", "set the plan branch; \"\" removes it")
	f.StringVar(&fl.timeout, "timeout", "", "set the timeout, such as 45m; \"\" removes it")
	f.StringArrayVar(&fl.repositories, "repository", nil, "set a repository's branch, name=branch; name= removes it (repeatable)")
	f.StringVar(&fl.required, "required", "", "variables a run must have, separated by commas; \"\" removes them")
	f.StringArrayVar(&fl.options, "option", nil, "set a variable's allowed values, name=a,b; name= removes them (repeatable)")
	f.StringVar(&fl.rename, "rename", "", "give the preset another name, and in the sets and pins that name it")
	f.BoolVar(&fl.dryRun, "dry-run", false, "show the changes and write nothing")
	return cmd
}

// given reports whether the command line names a change, which skips the form.
func (f *presetEditFlags) given(cmd *cobra.Command) bool {
	return len(f.vars) > 0 || len(f.unset) > 0 || len(f.repositories) > 0 || len(f.options) > 0 || f.rename != "" ||
		cmd.Flags().Changed("branch") || cmd.Flags().Changed("timeout") || cmd.Flags().Changed("required")
}

// flagEdit turns the flags into the edit. repos are the repositories the
// preset has now, whichever file holds them.
func (f *presetEditFlags) flagEdit(cmd *cobra.Command, repos map[string]string) (config.PresetEdit, error) {
	e := config.PresetEdit{Unset: f.unset, Rename: f.rename}
	for _, v := range f.vars {
		name, value, ok := strings.Cut(v, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return e, errs.Usagef("--var %q is not name=value", v).WithTry("--var ENV=prod")
		}
		e.Set = append(e.Set, config.VarValue{Name: strings.TrimSpace(name), Value: value})
	}
	for _, n := range f.unset {
		for _, v := range e.Set {
			if v.Name == n {
				return e, errs.Usagef("%s is in both --var and --unset", n)
			}
		}
	}
	if len(f.repositories) > 0 {
		want := maps.Clone(repos)
		if want == nil {
			want = map[string]string{}
		}
		for _, v := range f.repositories {
			name, branch, ok := strings.Cut(v, "=")
			if name = strings.TrimSpace(name); !ok || name == "" {
				return e, errs.Usagef("--repository %q is not name=branch", v).WithTry("--repository app=release-2, or --repository app= to remove it")
			}
			if branch = strings.TrimSpace(branch); branch == "" {
				delete(want, name)
			} else {
				want[name] = branch
			}
		}
		list := []config.RepoBranch{}
		for _, n := range slices.Sorted(maps.Keys(want)) {
			list = append(list, config.RepoBranch{Name: n, Branch: want[n]})
		}
		e.Repositories = &list
	}
	if cmd.Flags().Changed("required") {
		req := app.SplitList(f.required)
		e.Required = &req
	}
	for _, v := range f.options {
		name, list, ok := strings.Cut(v, "=")
		if name = strings.TrimSpace(name); !ok || name == "" {
			return e, errs.Usagef("--option %q is not name=a,b", v).WithTry("--option ENV=qa,prod, or --option ENV= to remove them")
		}
		if e.Options == nil {
			e.Options = map[string][]string{}
		}
		e.Options[name] = app.SplitList(list)
	}
	if cmd.Flags().Changed("branch") {
		e.Branch = &f.branch
	}
	if cmd.Flags().Changed("timeout") {
		e.Timeout = &f.timeout
	}
	return e, nil
}

func (f *presetEditFlags) run(r *runtime, cmd *cobra.Command, name string) error {
	cfg, err := r.config()
	if err != nil {
		return err
	}
	canAsk := !r.flags.json && r.tuiAllowed()
	asking := !f.given(cmd) && canAsk
	if name == "" {
		if name, err = r.pickPresetToEdit(cmd, cfg, canAsk, f.given(cmd)); err != nil {
			return err
		}
	}
	t, err := editablePreset(cfg, name)
	if err != nil {
		return err
	}
	layer := t.Layers[len(t.Layers)-1]
	// Anchors are refused before the form opens. A removal another file
	// blocks, and a rename bam knows it must refuse, are refused inside the form.
	if err := config.CheckEditable(layer.Path, layer.KeyPath, name); err != nil {
		return err
	}
	scope := r.renameScope(cfg, t)
	var e config.PresetEdit
	if asking {
		if e, err = f.fillForm(r, cmd, cfg, t, scope, layer.Path); err != nil {
			return err
		}
	} else if !f.given(cmd) {
		return errs.Usagef("bam preset edit %s has nothing to change", name).
			WithWhy("without a terminal there is no form, and no flag names a change").
			WithTry(fmt.Sprintf("bam preset edit %s --var NAME=value, or --unset, --branch, --repository, --required, --option, --timeout, --rename", name))
	} else if e, err = f.flagEdit(cmd, t.Repositories); err != nil {
		return err
	}
	if e.Rename != "" && e.Rename != name {
		if slices.Contains(takenNames(cfg), e.Rename) {
			return errs.Usagef("preset %q already exists", e.Rename).WithWhy("preset names are shared by every presets file")
		}
		if err := renameBlocked(cfg, t, scope); err != nil {
			return err
		}
	}
	changes, err := f.apply(cfg, t, scope, e)
	if err != nil {
		return err
	}
	if len(changes) > 0 && !f.dryRun {
		r.configChanged()
	}
	return f.print(r, t, layer.Path, e.Rename, changes, config.HeldBelow(layer.Target, t.Layers[:len(t.Layers)-1]))
}

// apply edits the file whose copy bam uses, checked as merged with the files
// below it. A rename also renames the preset in those files, and in the sets
// and pins that name it. Every file is checked before any is written, so a
// refusal leaves all of them as they were.
func (f *presetEditFlags) apply(cfg *config.Config, t config.ResolvedTarget, scope renameScope, e config.PresetEdit) ([]config.FieldChange, error) {
	top := t.Layers[len(t.Layers)-1]
	below := t.Layers[:len(t.Layers)-1]
	changes, err := config.EditTargetOver(top.Path, top.KeyPath, t.Name, below, e, false)
	if err != nil {
		return nil, err
	}
	renamed := e.Rename != "" && e.Rename != t.Name
	if renamed {
		for _, l := range below {
			if err := config.RenameTarget(l.Path, l.KeyPath, t.Name, e.Rename, false); err != nil {
				return nil, err
			}
		}
		refs, err := cfg.RenameRefs(t.Name, e.Rename, scope.shared, false)
		if err != nil {
			return nil, err
		}
		changes = append(changes, refs...)
		files, err := config.RenameInTrainFiles(scope.trains, t.Name, e.Rename, false)
		if err != nil {
			return nil, err
		}
		changes = append(changes, files...)
	}
	if f.dryRun || len(changes) == 0 {
		return changes, nil
	}
	// Train files are written first: they lie outside bam's own files, so a
	// failure there leaves every file bam owns as it was.
	type step struct {
		files []string
		run   func() error
	}
	var steps []step
	if renamed {
		for _, p := range changeFiles(changes, "train file") {
			steps = append(steps, step{[]string{p}, func() error {
				_, err := config.RenameInTrainFiles([]string{p}, t.Name, e.Rename, true)
				return err
			}})
		}
	}
	steps = append(steps, step{[]string{top.Path}, func() error {
		_, err := config.EditTargetOver(top.Path, top.KeyPath, t.Name, below, e, true)
		return err
	}})
	if renamed {
		for _, l := range below {
			steps = append(steps, step{[]string{l.Path}, func() error {
				return config.RenameTarget(l.Path, l.KeyPath, t.Name, e.Rename, true)
			}})
		}
		for _, p := range changeFiles(changes, "sets.", "monitor.") {
			steps = append(steps, step{[]string{p}, func() error {
				_, err := cfg.RenameRefsFiles([]string{p}, t.Name, e.Rename, scope.shared, true)
				return err
			}})
		}
	}
	var done []string
	for i, st := range steps {
		if err := st.run(); err != nil {
			if len(done) == 0 {
				return nil, err
			}
			var left []string
			for _, rest := range steps[i:] {
				for _, f := range rest.files {
					if !slices.Contains(left, f) {
						left = append(left, f)
					}
				}
			}
			// A file can have two steps, its preset copy and its sets or pins:
			// it stays under not written until its last step is done, and is
			// said to be partly written.
			var whole, partly []string
			for _, f := range done {
				if slices.Contains(left, f) {
					partly = append(partly, f)
				} else {
					whole = append(whole, f)
				}
			}
			why := fmt.Sprintf("%v; written: %s; not written: %s", err, strings.Join(whole, ", "), strings.Join(left, ", "))
			if len(partly) > 0 {
				why += "; partly written: " + strings.Join(partly, ", ")
			}
			return changes, errs.Configf("preset %s was only partly renamed", t.Name).
				WithWhy(why).
				WithTry(fmt.Sprintf("change %s to %s by hand in the files not written, then run bam preset list", t.Name, e.Rename))
		}
		for _, f := range st.files {
			if !slices.Contains(done, f) {
				done = append(done, f)
			}
		}
	}
	return changes, nil
}

// changeFiles lists each file a change whose field starts with one of the
// prefixes lands in, once.
func changeFiles(changes []config.FieldChange, prefixes ...string) []string {
	var out []string
	for _, c := range changes {
		if c.File != "" && !slices.Contains(out, c.File) && slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(c.Field, p) }) {
			out = append(out, c.File)
		}
	}
	return out
}

// renameScope is what a rename reaches beyond the preset's own file: the
// train files past set runs used, and whether the preset is shared by every
// repository, which carries the rename into their sets and runs.
type renameScope struct {
	trains []string
	shared bool
	// err is why the train files could not be found: a rename would leave
	// them on the old name without a word, so it is refused.
	err error
}

func (r *runtime) renameScope(cfg *config.Config, t config.ResolvedTarget) renameScope {
	s := renameScope{shared: cfg.Shared(t)}
	repo := cfg.RepoRoot
	if s.shared {
		repo = ""
	}
	var err error
	if s.trains, err = r.setStore().TrainFiles(repo); err != nil {
		s.err = errs.Configf("cannot rename %s: the record of past set runs cannot be read", t.Name).
			WithWhy(err.Error()).
			WithTry("fix or remove that folder to let bam find the train files, or rename the preset by hand")
	}
	return s
}

func (f *presetEditFlags) print(r *runtime, t config.ResolvedTarget, file, rename string, changes []config.FieldChange, held config.Held) error {
	masked := maskSecrets(changes)
	if r.flags.json {
		if masked == nil {
			masked = []config.FieldChange{}
		}
		return view.WriteJSON(r.env.Stdout, editDoc{Preset: t.Name, Renamed: rename, File: file, DryRun: f.dryRun, Changed: len(changes) > 0, Changes: masked})
	}
	w := r.env.Stdout
	if len(changes) == 0 {
		fmt.Fprintf(w, "preset %s already has these values; nothing changed in %s\n", t.Name, file)
		return nil
	}
	verb := "edited"
	if f.dryRun {
		verb = "would edit"
	}
	fmt.Fprintf(w, "%s preset %s in %s\n", verb, t.Name, file)
	for _, c := range masked {
		in := ""
		if c.File != "" {
			in = " (in " + c.File + ")"
		}
		switch {
		case c.Old == "" && c.New != "":
			fmt.Fprintf(w, "  %s: set to %q%s\n", c.Field, c.New, in)
		case c.New == "":
			fmt.Fprintf(w, "  %s: removed (was %q)%s\n", c.Field, c.Old, otherFileNote(held, c.Field))
		default:
			fmt.Fprintf(w, "  %s: %q -> %q%s\n", c.Field, c.Old, c.New, in)
		}
	}
	// The JSON lists the preset's own change once; the text also says which
	// lower files the rename writes, so a dry run shows every file it touches.
	if rename != "" && rename != t.Name {
		for _, l := range t.Layers[:len(t.Layers)-1] {
			fmt.Fprintf(w, "  name: %q -> %q (in %s)\n", t.Name, rename, l.Path)
		}
	}
	if f.dryRun {
		fmt.Fprintln(w, "dry run: nothing written")
	}
	return nil
}

// otherFileNote says whose value still applies after a removal, when another
// file holds one too.
func otherFileNote(held config.Held, field string) string {
	if file := held.Also[field]; file != "" {
		return "; the value in " + file + " applies"
	}
	return ""
}

// maskSecrets hides the old value of a secret variable: a preset written by
// hand may hold a literal one, and bam must not print it.
func maskSecrets(in []config.FieldChange) []config.FieldChange {
	var out []config.FieldChange
	for _, c := range in {
		if n, ok := strings.CutPrefix(c.Field, "variables."); ok && config.IsMaskedName(n) {
			if _, isRef := config.EnvRef(c.Old); !isRef && c.Old != "" {
				c.Old = "********"
			}
			if _, isRef := config.EnvRef(c.New); !isRef && c.New != "" {
				c.New = "********"
			}
		}
		out = append(out, c)
	}
	return out
}

// pickPresetToEdit is bare bam preset edit: on a terminal the picker of bare
// bam run; elsewhere a usage error, because a script must never wait on a key.
func (r *runtime) pickPresetToEdit(cmd *cobra.Command, cfg *config.Config, canAsk, given bool) (string, error) {
	if !canAsk || given {
		return "", errs.Usagef("bam preset edit needs a preset name").
			WithWhy("the picker needs a terminal, and this is a pipe, --json, --no-tui or a command line that names changes").
			WithTry("bam preset list to see the presets, then bam preset edit NAME")
	}
	items, err := app.DescribeTargets(cfg, r.env.Getenv)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		why, try := noPresetsMessage(cfg)
		return "", errs.Usagef("bam preset edit needs a preset name").WithWhy(why).WithTry(try)
	}
	return r.env.PickPreset(cmd.Context(), r.env.Stdin, r.env.Stdout, items, tui.EditWords)
}

// editablePreset finds preset name, or says why bam cannot edit it.
func editablePreset(cfg *config.Config, name string) (config.ResolvedTarget, error) {
	t, err := findPreset(cfg, name, "")
	if err == nil && len(t.Layers) == 0 {
		err = errs.Configf("preset %q has no file bam can edit", name)
	}
	return t, err
}

// renameBlocked says why preset t cannot be renamed, or nil when it can: a
// copy in a file that uses YAML anchors, or a set stage or pin that reaches
// the name through an anchor or alias, cannot be renamed in place. Sets and
// pins that name it are renamed with it.
func renameBlocked(cfg *config.Config, t config.ResolvedTarget, scope renameScope) error {
	for _, l := range t.Layers {
		if err := config.CheckEditable(l.Path, l.KeyPath, t.Name); err != nil {
			return err
		}
	}
	if scope.err != nil {
		return scope.err
	}
	// Any other name will do: the dry run fails on the old name's references only.
	if _, err := cfg.RenameRefs(t.Name, t.Name+"-renamed", scope.shared, false); err != nil {
		return err
	}
	_, err := config.RenameInTrainFiles(scope.trains, t.Name, t.Name+"-renamed", false)
	return err
}

// formText is an error as the form shows it, with its try line.
func formText(err error) string {
	var e *errs.Error
	if err == nil {
		return ""
	}
	if errors.As(err, &e) && e.Try != "" {
		return err.Error() + " (" + e.Try + ")"
	}
	return err.Error()
}

// takenNames are the names a preset cannot be renamed to.
func takenNames(cfg *config.Config) []string {
	all, _ := cfg.Targets()
	return slices.Sorted(maps.Keys(all))
}

// fillForm opens the edit form of t and returns the changes it holds.
// Bamboo is read only for the plan's variables; when it cannot be reached the
// form still opens with the variables the preset sets.
func (f *presetEditFlags) fillForm(r *runtime, cmd *cobra.Command, cfg *config.Config, t config.ResolvedTarget, scope renameScope, path string) (config.PresetEdit, error) {
	ctx := cmd.Context()
	planVars := map[string]string{}
	var svc *app.Service
	if s, _, err := r.connect(ctx, t.Server); err == nil {
		svc = s
		planVars = svc.PlanVariables(ctx, t.Plan)
	} else {
		r.note("could not read the plan's variables; the form shows only the preset's own", err.Error())
	}
	form := app.NewPresetEditForm(t, planVars)
	form.Path = path
	form.TakenNames = takenNames(cfg)
	form.RenameBlocked = formText(renameBlocked(cfg, t, scope))
	if svc != nil {
		form.ReadRepos = func(ctx context.Context, branch string) ([]app.RepoBranch, error) {
			return svc.PlanBranchRepositories(ctx, t.Plan, branch)
		}
	}
	form.Held = config.HeldBelow(t.Layers[len(t.Layers)-1].Target, t.Layers[:len(t.Layers)-1])
	edited, err := r.env.EditPreset(ctx, r.env.Stdin, r.env.Stdout, form, nil)
	if err != nil {
		return config.PresetEdit{}, askedCanceled(r, err)
	}
	return edited.Edit(), nil
}
