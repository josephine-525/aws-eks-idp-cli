package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

// Matches ci-templates/build-push.yml's include+extend usage for the build
// stage, and ci-templates/deploy-values.yml's .deploy_values (v1.1.0+) for
// the deploy stage -- the same shared template expenseapp's own
// deploy-values job extends. All the bump-tag/wait-for-sync/rollout-status
// logic lives there now, once, not duplicated per generated service.
//
// Deployment name assumes the Helm release name equals the ArgoCD
// Application name (both {{.Name}}), giving "<name>-common-web-service" —
// confirmed against expenseapp's real Deployment names
// (team-payments-backend-common-web-service, etc.), which follow the same
// "<release>-<chart>" fullname convention.
const gitlabCITemplate = `include:
  - project: 'demo-org/ci-templates'
    ref: v1.1.0
    file:
      - '/build-push.yml'
      - '/deploy-values.yml'

workflow:
  rules:
    - if: '$CI_PIPELINE_SOURCE == "web"'
    - when: never

stages:
  - build
  - deploy

build:
  extends: .build_push_image
  stage: build
  rules:
    - if: '$CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH'
  variables:
    DOCKERFILE_PATH: {{.DockerfilePath}}
    BUILD_CONTEXT: {{.BuildContext}}
    ECR_REPO: {{.ECRRepo}}

deploy:
  extends: .deploy_values
  stage: deploy
  rules:
    - if: '$CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH'
  needs:
    - job: build
  variables:
    VALUES_FILE: {{.ValuesFile}}
    ARGOCD_APP: {{.Name}}
    DEPLOYMENT_NAME: {{.Name}}-common-web-service
    K8S_NAMESPACE: {{.Team}}
    EKS_CLUSTER_NAME: {{.EKSClusterName}}
`

// ciVars is separate from ServiceSpec only where it needs a derived field
// (BuildContext isn't a flag, it's computed from --dockerfile-path; ValuesFile
// comes from spec.ValuesFile(), the one place that naming convention is
// decided, see values.go) — Name/Team/ECRRepo/EKSClusterName are plain
// passthroughs, kept here too just so this file has one self-contained view
// of what the template consumes.
type ciVars struct {
	Name           string
	Team           string
	DockerfilePath string
	BuildContext   string
	ECRRepo        string
	EKSClusterName string
	ValuesFile     string
}

func writeGitlabCI(spec ServiceSpec, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	t, err := template.New("gitlab-ci").Parse(gitlabCITemplate)
	if err != nil {
		return err
	}

	vars := ciVars{
		Name:           spec.Name,
		Team:           spec.Team,
		DockerfilePath: spec.DockerfilePath,
		BuildContext:   filepath.Dir(spec.DockerfilePath),
		ECRRepo:        spec.ECRRepo,
		EKSClusterName: spec.EKSClusterName,
		ValuesFile:     spec.ValuesFile(),
	}

	path := filepath.Join(outDir, ".gitlab-ci.yml")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := t.Execute(f, vars); err != nil {
		return err
	}

	fmt.Println("wrote", path)
	return nil
}

// Always generated alongside the full .gitlab-ci.yml above -- same
// principle as the ApplicationSet snippets: publish-service-mr never auto-patches
// an existing root .gitlab-ci.yml (same corruption risk as gitops's
// multi-document YAML), so if the target repo already has one, this is
// what a human pastes in instead. Job names are suffixed with the service
// name (build-{{.Name}}, not bare "build") since an existing file may
// already have jobs literally named "build"/"deploy" (expenseapp does).
const ciJobSnippetTemplate = `# Paste these two jobs into your existing .gitlab-ci.yml (repo root) --
# publish-service-mr found one already there, so it did NOT touch it.
#
# Before pasting, make sure that file's own include: already pulls in
# BOTH of these from ci-templates (add whichever is missing):
#   - project: 'demo-org/ci-templates'
#     ref: v1.1.0
#     file:
#       - '/build-push.yml'
#       - '/deploy-values.yml'
# And that its stages: list includes both build and deploy.

build-{{.Name}}:
  extends: .build_push_image
  stage: build
  rules:
    - if: '$CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH'
  variables:
    DOCKERFILE_PATH: {{.DockerfilePath}}
    BUILD_CONTEXT: {{.BuildContext}}
    ECR_REPO: {{.ECRRepo}}

deploy-{{.Name}}:
  extends: .deploy_values
  stage: deploy
  rules:
    - if: '$CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH'
  needs:
    - job: build-{{.Name}}
  variables:
    VALUES_FILE: {{.ValuesFile}}
    ARGOCD_APP: {{.Name}}
    DEPLOYMENT_NAME: {{.Name}}-common-web-service
    K8S_NAMESPACE: {{.Team}}
    EKS_CLUSTER_NAME: {{.EKSClusterName}}
`

func writeCIJobSnippet(spec ServiceSpec, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	t, err := template.New("ci-job-snippet").Parse(ciJobSnippetTemplate)
	if err != nil {
		return err
	}

	vars := ciVars{
		Name:           spec.Name,
		Team:           spec.Team,
		DockerfilePath: spec.DockerfilePath,
		BuildContext:   filepath.Dir(spec.DockerfilePath),
		ECRRepo:        spec.ECRRepo,
		EKSClusterName: spec.EKSClusterName,
		ValuesFile:     spec.ValuesFile(),
	}

	path := filepath.Join(outDir, "ci-job-snippet.yaml")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := t.Execute(f, vars); err != nil {
		return err
	}

	fmt.Println("wrote", path)
	return nil
}
