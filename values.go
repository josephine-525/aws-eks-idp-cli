package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

// Matches helm-charts/charts/common-web-service's values.yaml schema —
// only the fields a brand-new service actually needs on day one.
const valuesYAMLTemplate = `replicaCount: {{.Replicas}}

image:
  repository: ""  # TODO: fill in after the first image push, e.g. <account-id>.dkr.ecr.<region>.amazonaws.com/{{.ECRRepo}}
  tag: "latest"

service:
  targetPort: {{.Port}}

resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 256Mi

autoscaling:
  enabled: {{.HasHPA}}
  minReplicas: {{.Replicas}}
  maxReplicas: 5
  targetCPUUtilizationPercentage: 70
`

// ValuesFile is the ONE place this naming convention is decided -- always
// name-qualified, even for a repo's first/only service, so this never has
// to change later when a second service gets added. No more "default to
// values.yaml, rename on collision" -- that made the filename depend on
// what publish-service-mr discovers at push time, which is TOO LATE for the
// ApplicationSet snippet (already generated earlier, in this same job) to
// know about. Matches expenseapp's real values-backend.yaml/values-
// frontend.yaml, just applied from day one instead of only once a second
// service shows up.
//
// A method, not a free function: templates executed directly against a
// ServiceSpec (appset.go) can then just write {{.ValuesFile}} -- Go's
// text/template calls a zero-arg method the same way it reads a field.
func (s ServiceSpec) ValuesFile() string {
	return fmt.Sprintf("values-%s.yaml", s.Name)
}

func writeValuesYAML(spec ServiceSpec, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	t, err := template.New("values").Parse(valuesYAMLTemplate)
	if err != nil {
		return err
	}

	path := filepath.Join(outDir, spec.ValuesFile())
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
