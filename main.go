package main

import (
	"flag"
	"fmt"
	"os"
)

// ServiceSpec holds everything we know about the service being scaffolded,
// once flags are parsed and defaults are filled in.
type ServiceSpec struct {
	Name           string
	Team           string
	Port           int
	Replicas       int
	HasHPA         bool
	DockerfilePath string
	ECRRepo        string
	Dashboard      bool
	// DashboardTemplatePath points at a local checkout of the
	// observability-dashboards repo's templates/standard-microservice-
	// template.jsonnet -- the canonical copy; this tool never embeds or
	// vendors its own. Only read when Dashboard is true. In CI, the
	// generate job git-clones that repo fresh every run and this flag's
	// default already matches where it clones it to.
	DashboardTemplatePath string
	EKSClusterName        string
	// RepoName is the actual GitLab repo this service lives in -- defaults
	// to Name, but must be set explicitly (--repo-name) whenever the target
	// repo's name differs from the service name (e.g. onboarding
	// "analytics-backend" into the existing "analytics-demo" repo).
	// ApplicationSet snippets need the REAL repo name to point ArgoCD at,
	// not the service name -- getting this wrong means ArgoCD can't find
	// the values file at all.
	RepoName string
	// ECRRepoExists opts OUT of generating the Terraform ECR MR (defaults
	// to false, i.e. generate it) -- the common case for a brand-new
	// service is a brand-new repo (ECRRepo's own default already assumes
	// one repo per service), but set this true when deliberately reusing
	// an existing repo (e.g. a second service added to an already-wired
	// multi-service repo), where opening a duplicate "create this repo"
	// MR would be actively wrong, not just unnecessary.
	ECRRepoExists bool
}

func parseFlags() ServiceSpec {
	name := flag.String("name", "", "service name, e.g. team-payments-backend (required)")
	team := flag.String("team", "", "team/namespace this service belongs to (required)")
	port := flag.Int("port", 8080, "container port")
	replicas := flag.Int("replicas", -1, "replica count (default: 2 if --has-hpa, else 1)")
	hasHPA := flag.Bool("has-hpa", true, "generate HPA config in values.yaml")
	dockerfilePath := flag.String("dockerfile-path", "Dockerfile", "path to the Dockerfile inside the service's own repo")
	ecrRepo := flag.String("ecr-repo", "", "ECR repository name (defaults to --name)")
	repoName := flag.String("repo-name", "", "actual GitLab repo name this service lives in (defaults to --name; set this whenever the target repo isn't named after the service, e.g. onboarding into an existing multi-service repo)")
	dashboard := flag.Bool("dashboard", false, "also generate a starter Grafana dashboard skeleton")
	ecrRepoExists := flag.Bool("ecr-repo-exists", false, "set true if --ecr-repo already exists -- skips generating/opening the Terraform MR that would create it")
	dashboardTemplatePath := flag.String("dashboard-template", "obs-dash-repo/templates/standard-microservice-template.jsonnet", "path to a local checkout of observability-dashboards' templates/standard-microservice-template.jsonnet (only read when --dashboard=true)")
	eksClusterName := flag.String("eks-cluster-name", "demoapp-dev-expense-eks", "EKS cluster name, for the deploy job's kubeconfig update")

	flag.Parse()

	if *name == "" || *team == "" {
		fmt.Fprintln(os.Stderr, "error: --name and --team are required")
		flag.Usage()
		os.Exit(1)
	}

	if *ecrRepo == "" {
		*ecrRepo = *name
	}

	if *repoName == "" {
		*repoName = *name
	}

	if *replicas == -1 {
		if *hasHPA {
			*replicas = 2
		} else {
			*replicas = 1
		}
	}

	return ServiceSpec{
		Name:                  *name,
		Team:                  *team,
		Port:                  *port,
		Replicas:              *replicas,
		HasHPA:                *hasHPA,
		DockerfilePath:        *dockerfilePath,
		ECRRepo:               *ecrRepo,
		Dashboard:             *dashboard,
		DashboardTemplatePath: *dashboardTemplatePath,
		EKSClusterName:        *eksClusterName,
		RepoName:              *repoName,
		ECRRepoExists:         *ecrRepoExists,
	}
}

func main() {
	spec := parseFlags()
	fmt.Printf("%+v\n", spec)

	outDir := spec.Name
	if err := writeValuesYAML(spec, outDir); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := writeGitlabCI(spec, outDir); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := writeCIJobSnippet(spec, outDir); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := writeApplicationSetEntry(spec, outDir); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if !spec.ECRRepoExists {
		if err := writeTerraformECRSnippet(spec, outDir); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
	// Per-service, like --has-hpa -- no ownership check needed. See
	// dashboard.go for why this moved away from a per-team design (no
	// natural owner once a team's services span many repos). No
	// ApplicationSet snippet generated for it anymore either -- it's
	// published straight into the centralized observability-dashboards
	// repo by .gitlab-ci.yml's publish-dashboard-mr job, which a single
	// static ArgoCD Application already watches in full (see appset.go).
	if spec.Dashboard {
		if err := writeDashboard(spec, outDir); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
}
