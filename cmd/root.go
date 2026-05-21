package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/chzyer/readline"
	"github.com/gfouillet/wt-helper/internal/config"
	"github.com/gfouillet/wt-helper/internal/git"
	"github.com/gfouillet/wt-helper/internal/hooks"
	"github.com/gfouillet/wt-helper/internal/template"
	"github.com/spf13/cobra"
)

var (
	target         string
	envrcFile      string
	wrapupCmd      string
	force          bool
	varsFile       string
	auto           bool
	depDirs        []string
	depCopy        bool
	depInteractive bool
)

var rootCmd = &cobra.Command{
	Use:   "wt-helper",
	Short: "Manage .envrc and lifecycle hooks for git worktrees",
	Long: `wt-helper configures a git repository so that:
  - On worktree creation: a .envrc is copied and direnv-allowed
  - On worktree deletion: a wrapup command is executed before removal`,
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure a git repo for worktree envrc and lifecycle hooks",
	RunE:  runSetup,
}

var completionCmd = &cobra.Command{
	Use:       "completion [bash|zsh|fish|powershell]",
	Short:     "Generate shell completion scripts",
	ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
	Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	RunE:      runCompletion,
}

var renderTemplateCmd = &cobra.Command{
	Use:    "render-template [template-path]",
	Short:  "Render an envrc template (used internally by hooks)",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE:   runRenderTemplate,
}

var prepareVarsCmd = &cobra.Command{
	Use:    "prepare-vars [template-path]",
	Short:  "Interactively collect template vars and write merged file (used by wt-add alias)",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE:   runPrepareVars,
}

func init() {
	setupCmd.Flags().StringVar(&target, "target", "", "Path to git repo (default: current directory)")
	setupCmd.Flags().StringVar(&envrcFile, "envrc-file", "", "Path to source .envrc to copy on worktree creation (supports Go templates)")
	setupCmd.Flags().StringVar(&wrapupCmd, "wrapup-cmd", "", "Command/script to run on worktree deletion")
	setupCmd.Flags().BoolVar(&force, "force", false, "Overwrite existing hooks without prompting")
	setupCmd.Flags().StringVar(&varsFile, "vars-file", "", "Path to vars file for template rendering (JSON/TOML/YAML)")
	setupCmd.Flags().BoolVar(&auto, "auto", false, "Non-interactive mode: fail if template variables are missing")
	setupCmd.Flags().StringSliceVar(&depDirs, "dep-dir", nil, "Directory to symlink/copy into new worktrees (repeatable)")
	setupCmd.Flags().BoolVar(&depCopy, "dep-copy", false, "Deep copy dep dirs instead of symlinks")
	setupCmd.Flags().BoolVar(&depInteractive, "dep-interactive", false, "Interactively choose directories to link/copy")

	setupCmd.MarkFlagDirname("target")
	setupCmd.MarkFlagFilename("envrc-file")
	setupCmd.MarkFlagFilename("vars-file")
	setupCmd.RegisterFlagCompletionFunc("dep-dir", depDirCompletion)

	renderTemplateCmd.Flags().String("vars", "", "Path to vars file")
	renderTemplateCmd.Flags().String("worktree-path", "", "Worktree absolute path")
	renderTemplateCmd.Flags().String("branch", "", "Git branch name")
	renderTemplateCmd.Flags().Bool("interactive", false, "Prompt for missing template variables")

	prepareVarsCmd.Flags().String("vars", "", "Path to default vars file")
	prepareVarsCmd.Flags().String("output", "", "Path to write merged vars file")

	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(completionCmd)
	rootCmd.AddCommand(renderTemplateCmd)
	rootCmd.AddCommand(prepareVarsCmd)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func addToGitignore(repoRoot, entry string) error {
	giPath := filepath.Join(repoRoot, ".gitignore")
	data, err := os.ReadFile(giPath)
	if err == nil && strings.Contains(string(data), entry) {
		return nil // already there
	}
	f, err := os.OpenFile(giPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open .gitignore: %w", err)
	}
	defer f.Close()
	if len(data) > 0 && data[len(data)-1] != '\n' {
		fmt.Fprintln(f)
	}
	fmt.Fprintf(f, "%s\n", entry)
	fmt.Printf("wt-helper: added %s to .gitignore\n", entry)
	return nil
}

func runSetup(cmd *cobra.Command, args []string) error {
	if envrcFile == "" && wrapupCmd == "" {
		return fmt.Errorf("at least one of --envrc-file or --wrapup-cmd is required")
	}

	// Resolve target repo
	repoRoot := target
	if repoRoot == "" {
		var err error
		repoRoot, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}
	repoRoot, err := git.ResolveRepo(repoRoot)
	if err != nil {
		return err
	}

	gitDir, err := git.GitDir(repoRoot)
	if err != nil {
		return err
	}

	// Resolve helper binary path (needed for template hooks)
	helperBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve helper binary: %w", err)
	}
	helperBin, err = filepath.EvalSymlinks(helperBin)
	if err != nil {
		return fmt.Errorf("resolve helper binary symlink: %w", err)
	}

	// --- Copy source .envrc into .git/envrc-source ---
	var isTemplateMode bool
	if envrcFile != "" {
		absEnvrc, err := filepath.Abs(envrcFile)
		if err != nil {
			return fmt.Errorf("resolve envrc path: %w", err)
		}
		if _, err := os.Stat(absEnvrc); os.IsNotExist(err) {
			return fmt.Errorf("envrc file does not exist: %s", absEnvrc)
		}
		isTemplateMode = template.IsTemplate(absEnvrc)

		gitEnvrc := filepath.Join(gitDir, "envrc-source")
		if err := copyFile(absEnvrc, gitEnvrc); err != nil {
			return fmt.Errorf("copy envrc to .git/: %w", err)
		}
		fmt.Printf("wt-helper: copied envrc to %s\n", gitEnvrc)

		// Plain mode: copy .envrc to repo root and add to .gitignore
		if !isTemplateMode {
			envrcDest := filepath.Join(repoRoot, ".envrc")
			if err := copyFile(absEnvrc, envrcDest); err != nil {
				return fmt.Errorf("copy .envrc to repo root: %w", err)
			}
			fmt.Printf("wt-helper: copied .envrc to %s\n", envrcDest)

			if err := addToGitignore(repoRoot, ".envrc"); err != nil {
				return fmt.Errorf("update .gitignore: %w", err)
			}

			if direnvPath, err := exec.LookPath("direnv"); err == nil {
				exec.Command(direnvPath, "allow", repoRoot).Run()
				fmt.Println("wt-helper: direnv allow on main repo")
			}
		}
	}

	// --- Handle template vars ---
	var gitVarsFile string
	if isTemplateMode {
		fmt.Println("wt-helper: template mode enabled")

		// Resolve vars file
		var absVarsFile string
		if varsFile != "" {
			absVarsFile, err = filepath.Abs(varsFile)
			if err != nil {
				return fmt.Errorf("resolve vars file: %w", err)
			}
			if _, err := os.Stat(absVarsFile); os.IsNotExist(err) {
				return fmt.Errorf("vars file does not exist: %s", absVarsFile)
			}
		} else {
			detected := template.AutoDetectVarsFile(repoRoot)
			if detected != "" {
				absVarsFile = detected
				fmt.Printf("wt-helper: auto-detected vars file: %s\n", absVarsFile)
			}
		}

		// Copy vars file into .git/
		if absVarsFile != "" {
			ext := filepath.Ext(absVarsFile)
			gitVarsFile = filepath.Join(gitDir, "worktree-helper-vars"+ext)
			if err := copyFile(absVarsFile, gitVarsFile); err != nil {
				return fmt.Errorf("copy vars file to .git/: %w", err)
			}
			fmt.Printf("wt-helper: copied vars to %s\n", gitVarsFile)
		}

		// Load vars for validation and default rendering
		vars := make(map[string]string)
		if gitVarsFile != "" {
			vars, err = template.LoadVars(gitVarsFile)
			if err != nil {
				return fmt.Errorf("load vars: %w", err)
			}
		}

		// Validate vars completeness
		envrcSource := filepath.Join(gitDir, "envrc-source")
		if auto {
			needed, err := template.ExtractVars(envrcSource)
			if err != nil {
				return fmt.Errorf("extract template vars: %w", err)
			}
			for _, name := range needed {
				if _, ok := vars[name]; !ok {
					return fmt.Errorf("--auto mode: missing variable %q in vars file", name)
				}
			}
		} else {
			if err := template.PromptMissingVars(envrcSource, vars); err != nil {
				return fmt.Errorf("prompt for vars: %w", err)
			}
		}

		// Render default .envrc in repo root
		data := &template.TemplateData{
			WorktreeName: filepath.Base(repoRoot),
			WorktreePath: repoRoot,
			BranchName:   "main",
			Vars:         vars,
		}
		rendered, err := template.Render(envrcSource, data)
		if err != nil {
			return fmt.Errorf("render default .envrc: %w", err)
		}
		envrcDest := filepath.Join(repoRoot, ".envrc")
		if err := os.WriteFile(envrcDest, []byte(rendered), 0o644); err != nil {
			return fmt.Errorf("write default .envrc: %w", err)
		}
		fmt.Printf("wt-helper: rendered default .envrc at %s\n", envrcDest)

		// Add .envrc to .gitignore
		if err := addToGitignore(repoRoot, ".envrc"); err != nil {
			return fmt.Errorf("update .gitignore: %w", err)
		}

		// Run direnv allow on the main repo
		if direnvPath, err := exec.LookPath("direnv"); err == nil {
			exec.Command(direnvPath, "allow", repoRoot).Run()
			fmt.Println("wt-helper: direnv allow on main repo")
		}
	}

	// --- Validate wrapup command ---
	var resolvedWrapup string
	if wrapupCmd != "" {
		if strings.Contains(wrapupCmd, "/") {
			resolvedWrapup, err = filepath.Abs(wrapupCmd)
			if err != nil {
				return fmt.Errorf("resolve wrapup path: %w", err)
			}
			info, err := os.Stat(resolvedWrapup)
			if os.IsNotExist(err) {
				return fmt.Errorf("wrapup command does not exist: %s", resolvedWrapup)
			}
			if err != nil {
				return fmt.Errorf("stat wrapup command: %w", err)
			}
			if info.Mode()&0o111 == 0 {
				return fmt.Errorf("wrapup command is not executable: %s", resolvedWrapup)
			}
		} else {
			resolvedWrapup = wrapupCmd
		}
	}

	// --- Resolve dep dirs ---
	var resolvedDepDirs []string
	if depInteractive {
		dirs, err := promptDepDirs(repoRoot)
		if err != nil {
			return fmt.Errorf("interactive dep-dir selection: %w", err)
		}
		resolvedDepDirs = dirs
	} else {
		for _, d := range depDirs {
			abs := filepath.Join(repoRoot, d)
			info, err := os.Stat(abs)
			if os.IsNotExist(err) {
				return fmt.Errorf("dep-dir does not exist: %s", abs)
			}
			if err != nil {
				return fmt.Errorf("stat dep-dir: %w", err)
			}
			if !info.IsDir() {
				return fmt.Errorf("dep-dir is not a directory: %s", abs)
			}
			resolvedDepDirs = append(resolvedDepDirs, d)
		}
	}

	depMode := "symlink"
	if depCopy {
		depMode = "copy"
	}

	// --- Save config ---
	// Paths stored relative to gitDir (.git/)
	envrcSourceRel := ""
	if envrcFile != "" {
		envrcSourceRel = filepath.Join(gitDir, "envrc-source")
	}
	varsFileRel := gitVarsFile

	cfg := &config.Config{
		MainRepo:    repoRoot,
		EnvrcSource: envrcSourceRel,
		WrapupCmd:   resolvedWrapup,
		VarsFile:    varsFileRel,
		HelperBin:   helperBin,
		IsTemplate:  isTemplateMode,
		DepDirs:     strings.Join(resolvedDepDirs, ","),
		DepMode:     depMode,
	}
	if err := config.Save(repoRoot, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Printf("wt-helper: config written to %s\n", filepath.Join(gitDir, "worktree-helper.conf"))

	// --- Install post-checkout hook ---
	if envrcFile != "" {
		if err := hooks.InstallPostCheckout(gitDir, repoRoot, force); err != nil {
			return fmt.Errorf("install post-checkout hook: %w", err)
		}
	}

	// --- Install wt-remove alias ---
	if wrapupCmd != "" {
		if err := hooks.InstallWtRemoveAlias(repoRoot); err != nil {
			return fmt.Errorf("install wt-remove alias: %w", err)
		}
	}

	// --- Install wt-add alias (template mode only) ---
	if isTemplateMode {
		if err := hooks.InstallWtAddAlias(repoRoot); err != nil {
			return fmt.Errorf("install wt-add alias: %w", err)
		}
	}

	// --- Summary ---
	fmt.Println("\nwt-helper: setup complete!")
	if envrcFile != "" {
		if isTemplateMode {
			fmt.Printf("  template:     %s\n", filepath.Join(gitDir, "envrc-source"))
			if gitVarsFile != "" {
				fmt.Printf("  vars file:    %s\n", gitVarsFile)
			}
			fmt.Printf("  default .envrc: %s (git-ignored)\n", filepath.Join(repoRoot, ".envrc"))
		} else {
			fmt.Printf("  envrc source: %s\n", filepath.Join(gitDir, "envrc-source"))
		}
	}
	if wrapupCmd != "" {
		fmt.Printf("  wrapup cmd:   %s\n", resolvedWrapup)
		fmt.Println("  deletion:     git wt-remove <worktree-path>")
	}
	if isTemplateMode {
		fmt.Println("  creation:     git wt-add <worktree-path> -b <branch>")
	}
	if len(resolvedDepDirs) > 0 {
		fmt.Printf("  dep dirs:     %s (%s)\n", strings.Join(resolvedDepDirs, ", "), depMode)
	}
	return nil
}

func runRenderTemplate(cmd *cobra.Command, args []string) error {
	templatePath := args[0]

	varsPath, _ := cmd.Flags().GetString("vars")
	worktreePath, _ := cmd.Flags().GetString("worktree-path")
	branchName, _ := cmd.Flags().GetString("branch")
	interactive, _ := cmd.Flags().GetBool("interactive")

	if worktreePath == "" {
		var err error
		worktreePath, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}

	vars := make(map[string]string)
	if varsPath != "" {
		var err error
		vars, err = template.LoadVars(varsPath)
		if err != nil {
			return fmt.Errorf("load vars: %w", err)
		}
	}

	if interactive {
		// Prompt for missing vars; silently skip if stdin is not a terminal
		_ = template.PromptMissingVars(templatePath, vars)
	}

	data := &template.TemplateData{
		WorktreeName: filepath.Base(worktreePath),
		WorktreePath: worktreePath,
		BranchName:   branchName,
		Vars:         vars,
	}

	rendered, err := template.Render(templatePath, data)
	if err != nil {
		return fmt.Errorf("render template: %w", err)
	}

	fmt.Print(rendered)
	return nil
}

func runPrepareVars(cmd *cobra.Command, args []string) error {
	templatePath := args[0]
	varsPath, _ := cmd.Flags().GetString("vars")
	outputPath, _ := cmd.Flags().GetString("output")

	if outputPath == "" {
		return fmt.Errorf("--output is required")
	}

	vars := make(map[string]string)
	if varsPath != "" {
		var err error
		vars, err = template.LoadVars(varsPath)
		if err != nil {
			return fmt.Errorf("load vars: %w", err)
		}
	}

	if err := template.WriteMergedVars(templatePath, vars, outputPath); err != nil {
		return fmt.Errorf("write merged vars: %w", err)
	}
	return nil
}

func listRepoDirs(repoRoot string) []string {
	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != ".git" {
			dirs = append(dirs, e.Name())
		}
	}
	return dirs
}

type dirCompleter struct {
	repoRoot string
}

func (c dirCompleter) Do(line []rune, pos int) ([][]rune, int) {
	prefix := string(line[:pos])
	dirs := listRepoDirs(c.repoRoot)
	var matches []string
	for _, d := range dirs {
		if strings.HasPrefix(d, prefix) {
			matches = append(matches, d)
		}
	}
	result := make([][]rune, 0, len(matches))
	for _, m := range matches {
		result = append(result, []rune(m[len(prefix):]))
	}
	return result, len(prefix)
}

func promptDepDirs(repoRoot string) ([]string, error) {
	fmt.Println("Select dep dirs (TAB to complete, enter empty to finish):")

	rl, err := readline.NewEx(&readline.Config{
		Prompt:       "  dir > ",
		AutoComplete: dirCompleter{repoRoot: repoRoot},
	})
	if err != nil {
		return nil, fmt.Errorf("readline: %w", err)
	}
	defer rl.Close()

	var result []string
	for {
		d, err := rl.Readline()
		if err == io.EOF || d == "" {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("readline: %w", err)
		}
		d = strings.TrimSpace(d)
		if d == "" {
			break
		}
		abs := filepath.Join(repoRoot, d)
		info, err := os.Stat(abs)
		if os.IsNotExist(err) {
			fmt.Printf("    %s: directory not found\n", d)
			continue
		}
		if err != nil {
			fmt.Printf("    %s: %v\n", d, err)
			continue
		}
		if !info.IsDir() {
			fmt.Printf("    %s: not a directory\n", d)
			continue
		}
		result = append(result, d)
	}
	return result, nil
}

func depDirCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	repoRoot, _ := cmd.Flags().GetString("target")
	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}
	repoRoot, err := git.ResolveRepo(repoRoot)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	dirs := listRepoDirs(repoRoot)
	return dirs, cobra.ShellCompDirectiveNoFileComp
}

func runCompletion(cmd *cobra.Command, args []string) error {
	switch args[0] {
	case "bash":
		return rootCmd.GenBashCompletionV2(os.Stdout, true)
	case "zsh":
		return rootCmd.GenZshCompletion(os.Stdout)
	case "fish":
		return rootCmd.GenFishCompletion(os.Stdout, true)
	case "powershell":
		return rootCmd.GenPowerShellCompletion(os.Stdout)
	}
	return nil
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
