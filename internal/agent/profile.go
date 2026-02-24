package agent

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	stokerv1alpha1 "github.com/ia-eknorr/stoker-operator/api/v1alpha1"
	"github.com/ia-eknorr/stoker-operator/internal/syncengine"
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

// fetchSyncProfile reads a SyncProfile CR from the K8s API.
func fetchSyncProfile(ctx context.Context, c client.Client, namespace, name string) (*stokerv1alpha1.SyncProfileSpec, error) {
	sp := &stokerv1alpha1.SyncProfile{}
	key := types.NamespacedName{Name: name, Namespace: namespace}

	if err := c.Get(ctx, key, sp); err != nil {
		return nil, fmt.Errorf("fetching SyncProfile %s/%s: %w", namespace, name, err)
	}
	return &sp.Spec, nil
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

// buildSyncPlan constructs a SyncPlan from a SyncProfile spec, template context,
// and runtime paths.
func buildSyncPlan(
	profile *stokerv1alpha1.SyncProfileSpec,
	tmplCtx *TemplateContext,
	repoPath string,
	liveDir string,
	crExcludes []string,
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

	// Merge excludes from three sources: engine defaults, profile, CR.
	allExcludes := make([]string, 0, len(crExcludes)+len(profile.ExcludePatterns))
	allExcludes = append(allExcludes, crExcludes...)
	allExcludes = append(allExcludes, profile.ExcludePatterns...)
	plan.ExcludePatterns = allExcludes

	return plan, nil
}

// parseCRExcludes splits a comma-separated exclude patterns string.
func parseCRExcludes(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
