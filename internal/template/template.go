package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

var builtinVars = map[string]bool{
	"WorktreeName": true,
	"WorktreePath": true,
	"BranchName":   true,
}

type TemplateData struct {
	WorktreeName string
	WorktreePath string
	BranchName   string
	Vars         map[string]string
}

func IsTemplate(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "{{")
}

func Render(templatePath string, data *TemplateData) (string, error) {
	content, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("read template: %w", err)
	}

	funcMap := template.FuncMap{
		"env": func(key string) string {
			return os.Getenv(key)
		},
	}

	tmpl, err := template.New(filepath.Base(templatePath)).Funcs(funcMap).Parse(string(content))
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	vars := make(map[string]string)
	if data.Vars != nil {
		for k, v := range data.Vars {
			vars[k] = v
		}
	}

	// Overlay env vars — env takes precedence over vars file
	needed, _ := ExtractVars(templatePath)
	for _, name := range needed {
		if val, ok := os.LookupEnv(name); ok {
			vars[name] = val
		}
	}

	tmplData := map[string]string{
		"WorktreeName": data.WorktreeName,
		"WorktreePath": data.WorktreePath,
		"BranchName":   data.BranchName,
	}
	for k, v := range vars {
		tmplData[k] = v
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, tmplData); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

func LoadVars(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read vars file: %w", err)
	}

	var raw map[string]interface{}
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".json":
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse JSON: %w", err)
		}
	case ".toml":
		if err := toml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse TOML: %w", err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse YAML: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported vars file format: %s (use .json, .toml, or .yaml)", ext)
	}

	vars := make(map[string]string)
	for k, v := range raw {
		vars[k] = fmt.Sprintf("%v", v)
	}
	return vars, nil
}

var templateVarRe = regexp.MustCompile(`\{\{\s*\.(\w+)`)

func ExtractVars(templatePath string) ([]string, error) {
	data, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, fmt.Errorf("read template: %w", err)
	}

	matches := templateVarRe.FindAllStringSubmatch(string(data), -1)
	seen := make(map[string]bool)
	var vars []string
	for _, m := range matches {
		name := m[1]
		if !seen[name] && !builtinVars[name] {
			vars = append(vars, name)
			seen[name] = true
		}
	}
	return vars, nil
}

func PromptMissingVars(templatePath string, vars map[string]string) error {
	needed, err := ExtractVars(templatePath)
	if err != nil {
		return err
	}

	for _, name := range needed {
		if _, ok := vars[name]; ok {
			continue
		}
		// Check env var before prompting
		if val, ok := os.LookupEnv(name); ok {
			vars[name] = val
			continue
		}
		fmt.Printf("  %s = ", name)
		var val string
		if _, err := fmt.Scanln(&val); err != nil {
			return fmt.Errorf("read input for %s: %w", name, err)
		}
		vars[name] = val
	}
	return nil
}

func WriteMergedVars(templatePath string, vars map[string]string, outputPath string) error {
	needed, err := ExtractVars(templatePath)
	if err != nil {
		return err
	}

	for _, name := range needed {
		// Check env var first
		if val, ok := os.LookupEnv(name); ok {
			vars[name] = val
			continue
		}
		// Show default if exists, prompt for override
		if def, ok := vars[name]; ok {
			fmt.Printf("  %s [%s] = ", name, def)
		} else {
			fmt.Printf("  %s = ", name)
		}
		var val string
		if _, err := fmt.Scanln(&val); err != nil {
			// stdin not available or empty — keep default
			fmt.Println()
			continue
		}
		if val != "" {
			vars[name] = val
		}
	}

	// Write the merged vars as JSON
	data, err := json.MarshalIndent(vars, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal vars: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	return os.WriteFile(outputPath, data, 0o644)
}

func AutoDetectVarsFile(repoRoot string) string {
	candidates := []string{
		".wt-helper.vars.json",
		".wt-helper.vars.toml",
		".wt-helper.vars.yaml",
		".wt-helper.vars.yml",
	}
	for _, name := range candidates {
		path := filepath.Join(repoRoot, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}
