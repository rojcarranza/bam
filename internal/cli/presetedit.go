package cli

import (
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
		Long: "Change a saved preset without opening its YAML. bam rewrites only the lines that change, in the\n" +
			"file whose copy of the preset bam uses, and keeps comments, key order and the other presets.\n" +
			"On a terminal, bam preset edit [NAME] opens a form filled with the saved preset: its variables,\n" +
			"the plan variables it leaves out (written only once you change them), then name, branch,\n" +
			"repositories, required and timeout. enter edits a row, ←→ step through the choices, x removes a\n" +
			"variable so the plan's own value applies, o edits a variable's options, ctrl-s saves and esc\n" +
			"writes nothing.\n" +
			"With --var, --unset, --branch, --timeout or --rename there is no form: bam applies them and\n" +
			"never asks, so scripts and agents can use it. --dry-run shows the changes and writes nothing.\n" +
			"A secret's value is never written: give ${ENV_NAME} and bam reads it from your shell at run\n" +
			"time. A preset that uses YAML anchors is refused, and so is a rename of a preset a set uses.",
		Example: "  bam preset edit\n" +
			"  bam preset edit deploy-qa\n" +
			"  bam preset edit deploy-qa --var ENV=prod --unset REGION\n" +
			"  bam preset edit deploy-qa --branch release-2 --timeout 1h --dry-run\n" +
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
	f.StringVar(&fl.rename, "rename", "", "give the preset another name")
	f.BoolVar(&fl.dryRun, "dry-run", false, "show the changes and write nothing")
	return cmd
}

// given reports whether the command line names a change, which skips the form.
func (f *presetEditFlags) given(cmd *cobra.Command) bool {
	return len(f.vars) > 0 || len(f.unset) > 0 || cmd.Flags().Changed("branch") || cmd.Flags().Changed("timeout") || f.rename != ""
}

// flagEdit turns the flags into the edit.
func (f *presetEditFlags) flagEdit(cmd *cobra.Command) (config.PresetEdit, error) {
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
	// blocks, and a rename that is refused, are refused inside the form.
	if err := config.CheckEditable(layer.Path, layer.KeyPath, name); err != nil {
		return err
	}
	var e config.PresetEdit
	if asking {
		if e, err = f.fillForm(r, cmd, cfg, t, layer.Path); err != nil {
			return err
		}
	} else if !f.given(cmd) {
		return errs.Usagef("bam preset edit %s has nothing to change", name).
			WithWhy("without a terminal there is no form, and no flag names a change").
			WithTry(fmt.Sprintf("bam preset edit %s --var NAME=value, or --unset, --branch, --timeout, --rename", name))
	} else if e, err = f.flagEdit(cmd); err != nil {
		return err
	}
	if e.Rename != "" && e.Rename != name {
		if slices.Contains(takenNames(cfg), e.Rename) {
			return errs.Usagef("preset %q already exists", e.Rename).WithWhy("preset names are shared by every presets file")
		}
		if err := renameBlocked(cfg, t); err != nil {
			return err
		}
	}
	changes, err := f.apply(t, e)
	if err != nil {
		return err
	}
	if len(changes) > 0 && !f.dryRun {
		r.configChanged()
	}
	if e.Rename != "" && len(changes) > 0 && !f.dryRun {
		r.notePinned(cfg, name)
	}
	return f.print(r, t, layer.Path, e.Rename, changes)
}

// apply edits the file whose copy bam uses, checked as merged with the files
// below it. A rename also renames the preset in those files. Every file is
// checked before any is written, so a refusal leaves all of them as they were.
func (f *presetEditFlags) apply(t config.ResolvedTarget, e config.PresetEdit) ([]config.FieldChange, error) {
	top := t.Layers[len(t.Layers)-1]
	below := t.Layers[:len(t.Layers)-1]
	changes, err := config.EditTargetOver(top.Path, top.KeyPath, t.Name, below, e, false)
	if err != nil || f.dryRun || len(changes) == 0 {
		return changes, err
	}
	renamed := e.Rename != "" && e.Rename != t.Name
	if renamed {
		for _, l := range below {
			if err := config.RenameTarget(l.Path, l.KeyPath, t.Name, e.Rename, false); err != nil {
				return nil, err
			}
		}
	}
	if _, err := config.EditTargetOver(top.Path, top.KeyPath, t.Name, below, e, true); err != nil {
		return nil, err
	}
	if renamed {
		for _, l := range below {
			if err := config.RenameTarget(l.Path, l.KeyPath, t.Name, e.Rename, true); err != nil {
				return changes, err
			}
		}
	}
	return changes, nil
}

func (f *presetEditFlags) print(r *runtime, t config.ResolvedTarget, file, rename string, changes []config.FieldChange) error {
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
		switch {
		case c.Old == "" && c.New != "":
			fmt.Fprintf(w, "  %s: set to %q\n", c.Field, c.New)
		case c.New == "":
			fmt.Fprintf(w, "  %s: removed (was %q)\n", c.Field, c.Old)
		default:
			fmt.Fprintf(w, "  %s: %q -> %q\n", c.Field, c.Old, c.New)
		}
	}
	if f.dryRun {
		fmt.Fprintln(w, "dry run: nothing written")
	}
	return nil
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
// set that runs it would fail, and a copy in a file that uses YAML anchors
// cannot be renamed in place.
func renameBlocked(cfg *config.Config, t config.ResolvedTarget) error {
	if err := presetUsedBySet(cfg, t.Name, "change the set first, or leave the name as it is"); err != nil {
		return err
	}
	for _, l := range t.Layers {
		if err := config.CheckEditable(l.Path, l.KeyPath, t.Name); err != nil {
			return err
		}
	}
	return nil
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
func (f *presetEditFlags) fillForm(r *runtime, cmd *cobra.Command, cfg *config.Config, t config.ResolvedTarget, path string) (config.PresetEdit, error) {
	ctx := cmd.Context()
	planVars := map[string]string{}
	if svc, _, err := r.connect(ctx, t.Server); err == nil {
		planVars = svc.PlanVariables(ctx, t.Plan)
	} else {
		r.note("could not read the plan's variables; the form shows only the preset's own", err.Error())
	}
	form := app.NewPresetEditForm(t, planVars)
	form.Path = path
	form.TakenNames = takenNames(cfg)
	form.RenameBlocked = formText(renameBlocked(cfg, t))
	form.Held = app.Held(config.HeldBelow(t.Layers[len(t.Layers)-1].Target, t.Layers[:len(t.Layers)-1]))
	edited, err := r.env.EditPreset(ctx, r.env.Stdin, r.env.Stdout, form, nil)
	if err != nil {
		return config.PresetEdit{}, askedCanceled(r, err)
	}
	return edited.Edit(), nil
}
