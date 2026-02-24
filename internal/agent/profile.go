package agent

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/ia-eknorr/stoker-operator/internal/syncengine"
	stokertypes "github.com/ia-eknorr/stoker-operator/pkg/types"
)

// TemplateContext holds the variables available in mapping templates.
type TemplateContext struct {
	GatewayName string
	Namespace   string
	Ref         string
	Commit      string
	CRName      string
	Labels      map[string]string
	Vars        map[string]string
}

// buildTemplateContext creates a TemplateContext from agent config, metadata, and pod labels.
func buildTemplateContext(cfg *Config, meta *Metadata, profileVars map[string]string, labels map[string]string) *TemplateContext {
	vars := make(map[string]string, len(profileVars))
	maps.Copy(vars, profileVars)
	podLabels := make(map[string]string, len(labels))
	maps.Copy(podLabels, labels)
	return &TemplateContext{
		GatewayName: cfg.GatewayName,
		Namespace:   cfg.CRNamespace,
		Ref:         meta.Ref,
		Commit:      meta.Commit,
		CRName:      cfg.CRName,
		Labels:      podLabels,
		Vars:        vars,
	}
}

// resolveTemplate resolves a Go template string using the given context.
// Returns an error if any referenced key is missing.
func resolveTemplate(tmpl string, ctx *TemplateContext) (string, error) {
	// Fast path: no template syntax.
	if !strings.Contains(tmpl, "{{") {
		return tmpl, nil
	}

	t, err := template.New("").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parsing template %q: %w", tmpl, err)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("executing template %q: %w", tmpl, err)
	}
	return buf.String(), nil
}

// validateResolvedPath rejects paths with traversal or absolute components.
func validateResolvedPath(path, label string) error {
	if filepath.IsAbs(path) {
		return fmt.Errorf("%s: absolute path not allowed: %s", label, path)
	}
	cleaned := filepath.ToSlash(filepath.Clean(path))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return fmt.Errorf("%s: path traversal not allowed: %s", label, path)
	}
	return nil
}

// buildSyncPlan constructs a SyncPlan from a resolved profile, template context,
// and runtime paths. The profile already has defaults merged by the controller.
func buildSyncPlan(
	profile *stokertypes.ResolvedProfile,
	tmplCtx *TemplateContext,
	repoPath string,
	liveDir string,
) (*syncengine.SyncPlan, error) {
	stagingDir := filepath.Join(liveDir, ".sync-staging")

	plan := &syncengine.SyncPlan{
		StagingDir: stagingDir,
		LiveDir:    liveDir,
		DryRun:     profile.DryRun,
	}

	// Resolve and validate each mapping.
	for i, m := range profile.Mappings {
		src, err := resolveTemplate(m.Source, tmplCtx)
		if err != nil {
			return nil, fmt.Errorf("mapping[%d].source: %w", i, err)
		}
		dst, err := resolveTemplate(m.Destination, tmplCtx)
		if err != nil {
			return nil, fmt.Errorf("mapping[%d].destination: %w", i, err)
		}

		if err := validateResolvedPath(src, fmt.Sprintf("mapping[%d].source", i)); err != nil {
			return nil, err
		}
		if err := validateResolvedPath(dst, fmt.Sprintf("mapping[%d].destination", i)); err != nil {
			return nil, err
		}

		absSrc := filepath.Join(repoPath, src)

		// Check required flag.
		if m.Required {
			if _, err := os.Stat(absSrc); os.IsNotExist(err) {
				return nil, fmt.Errorf("mapping[%d]: required source does not exist: %s", i, src)
			}
		}

		typ := m.Type
		if typ == "" {
			typ = "dir"
		}

		plan.Mappings = append(plan.Mappings, syncengine.ResolvedMapping{
			Source:      absSrc,
			Destination: dst,
			Type:        typ,
		})
	}

	// Excludes already merged by controller (defaults + profile).
	plan.ExcludePatterns = profile.ExcludePatterns

	return plan, nil
}
