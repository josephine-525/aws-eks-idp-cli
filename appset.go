package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

// Written as a real, standalone ApplicationSet entry -- not a snippet for a
// human to paste into gitops/applicationsets/expense-teams.yaml. That file
// used to hold a hand-maintained List generator (every new service meant
// editing a shared YAML list by hand, since a script once corrupted that
// same file by string-patching it instead of doing a real YAML parse --
// see CLAUDE.md/memory), which doesn't scale once there are many teams with
// many services each. It's now a Git files generator instead
// (path: services/**/*.yaml): this file gets copied, unmodified, into
// gitops's own services/<team>/<name>.yaml by .gitlab-ci.yml's
// publish-appset-mr job, as its own MR -- "new service" becomes "one new
// file added to a directory," never a shared list edited in place, so the
// old corruption risk doesn't apply to this mechanism at all.
//
// Field names/values match exactly what the old List generator's elements
// carried (name/namespace/project/valuesRepoURL/valuesFile) -- the
// ApplicationSet's own template block didn't need to change at all when
// the generator did, since it already used goTemplate: true.
const applicationSetEntryTemplate = `name: {{.Name}}
namespace: {{.Team}}
project: {{.Team}}
valuesRepoURL: https://gitlab.com/demo-org/{{.RepoName}}.git
valuesFile: {{.ValuesFile}}
`

func writeApplicationSetEntry(spec ServiceSpec, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	t, err := template.New("appset-entry").Parse(applicationSetEntryTemplate)
	if err != nil {
		return err
	}

	path := filepath.Join(outDir, "applicationset-entry.yaml")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := t.Execute(f, spec); err != nil {
		return err
	}

	fmt.Println("wrote", path)
	return nil
}

// No dashboard equivalent of this function anymore. Dashboards used to need
// their own per-service ApplicationSet element (one repo's dashboards/ dir
// per element) -- now that they're centralized in the observability-
// dashboards repo (see dashboard.go), a single static ArgoCD Application
// with directory.recurse: true syncs the whole teams/ tree, so no
// per-service GitOps wiring is generated at all. The generated dashboard
// file itself is published straight into that repo by .gitlab-ci.yml's
// publish-dashboard-mr job, as a second MR alongside the service's own.
