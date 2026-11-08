package api

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	apispec "cloud/api"
)

func TestOpenAPIMatchesRoutes(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(apispec.Spec, &spec); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	var documented []string
	for path, item := range spec.Paths {
		for method := range item {
			if method != "parameters" {
				documented = append(documented, strings.ToUpper(method)+" "+path)
			}
		}
	}

	var registered []string
	for _, r := range (&Server{}).routes() {
		if _, path, _ := strings.Cut(r.pattern, " "); strings.HasPrefix(path, "/api/") {
			registered = append(registered, r.pattern)
		}
	}

	for _, r := range registered {
		if !slices.Contains(documented, r) {
			t.Errorf("route %q is not documented in openapi.yaml", r)
		}
	}
	for _, d := range documented {
		if !slices.Contains(registered, d) {
			t.Errorf("openapi.yaml documents %q, but no such route is registered", d)
		}
	}
}
