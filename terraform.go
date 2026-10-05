package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// TerraformResourceName is ECRRepo with hyphens swapped for underscores --
// Terraform resource/output names can't contain hyphens, but ECR repository
// names commonly do (this project's own convention: "fraud-detection", not
// "fraud_detection"). A method, not a free function, for the same reason
// ValuesFile() is one: text/template calls a zero-arg method on its context
// value the same way it reads a field.
func (s ServiceSpec) TerraformResourceName() string {
	return strings.ReplaceAll(s.ECRRepo, "-", "_")
}

// Written as a complete, standalone .tf file -- not a snippet for a human to
// paste into terraform-modules/ecr/main.tf. Terraform merges every .tf file
// in a module directory automatically, so a new file here needs zero edits
// to the existing backend/frontend resources in main.tf -- same "new file,
// never touch existing content" principle as applicationset-entry.yaml and
// the dashboard skeleton, applied here for the same reason: editing a
// shared file by hand/by script is exactly the class of risk this tool
// avoids everywhere else (see CLAUDE.md's expense-teams.yaml corruption
// story).
//
// This MR only wires the repository into the ecr module itself. It does
// NOT bump the module's semver tag or update expenseinfra's account/ecr
// call site (?ref=, plus publishing this output via SSM) -- both stay
// manual, same as this project's own stated versioning convention (plain
// `git tag`, bumped deliberately, no automation). The publish job's MR
// description spells out those two remaining steps so they're not a
// silent gap.
const terraformECRTemplate = `# {{.Name}}'s own ECR repository -- scaffolded by idp-cli.
resource "aws_ecr_repository" "{{.TerraformResourceName}}" {
  name                 = "${var.name_prefix}-{{.ECRRepo}}"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  image_scanning_configuration {
    scan_on_push = true
  }
}

output "{{.TerraformResourceName}}_repository_url" {
  value = aws_ecr_repository.{{.TerraformResourceName}}.repository_url
}
`

func writeTerraformECRSnippet(spec ServiceSpec, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	t, err := template.New("terraform-ecr").Parse(terraformECRTemplate)
	if err != nil {
		return err
	}

	path := filepath.Join(outDir, "terraform-ecr.tf")
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
