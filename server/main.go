package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// This project's group-and-path, not a numeric ID -- GitLab's API accepts
// a URL-encoded "namespace/project" path anywhere it takes a project :id.
const gitlabProjectPath = "demo-org/idp-cli"

//go:embed static
var embeddedStatic embed.FS

// generateRequest mirrors idp-cli's own spec:inputs (.gitlab-ci.yml) field
// for field -- this server's only job is collecting these from a real web
// form and handing them to GitLab's pipeline API, not re-deciding what the
// pipeline itself needs.
type generateRequest struct {
	Name           string `json:"name"`
	Team           string `json:"team"`
	Port           int    `json:"port"`
	HasHPA         bool   `json:"has_hpa"`
	Replicas       string `json:"replicas"`
	DockerfilePath string `json:"dockerfile_path"`
	ECRRepo        string `json:"ecr_repo"`
	ECRRepoExists  bool   `json:"ecr_repo_exists"`
	Dashboard      bool   `json:"dashboard"`
	EKSClusterName string `json:"eks_cluster_name"`
	TargetRepo     string `json:"target_repo"`
	TargetPath     string `json:"target_path"`
}

func main() {
	token := os.Getenv("IDP_CLI_PUSH_TOKEN")
	if token == "" {
		log.Fatal("set IDP_CLI_PUSH_TOKEN (the same api-scoped group Developer token used by publish-service-mr)")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	staticRoot, err := fs.Sub(embeddedStatic, "static")
	if err != nil {
		log.Fatal(err)
	}

	// Not required at startup (unlike IDP_CLI_PUSH_TOKEN above) -- someone
	// using only the plain form shouldn't be blocked from starting the
	// server just because they haven't set this up. handleChat checks for
	// it itself and returns a clear error on that route specifically, same
	// reasoning as GITLAB_TOKEN in gitlab.go.
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticRoot)))
	mux.HandleFunc("/generate", handleGenerate(token))
	mux.HandleFunc("/chat", handleChat(anthropicKey))
	mux.HandleFunc("/status", handleStatus(token))

	addr := "127.0.0.1:" + port
	log.Printf("listening on http://%s (local only -- not exposed beyond this machine)", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func handleGenerate(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req generateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}

		if req.Name == "" || req.Team == "" {
			respondError(w, http.StatusBadRequest, "name and team are required")
			return
		}

		inputs := map[string]any{
			"name":             req.Name,
			"team":             req.Team,
			"port":             req.Port,
			"has_hpa":          req.HasHPA,
			"replicas":         req.Replicas,
			"dockerfile_path":  req.DockerfilePath,
			"ecr_repo":         req.ECRRepo,
			"ecr_repo_exists":  req.ECRRepoExists,
			"dashboard":        req.Dashboard,
			"eks_cluster_name": req.EKSClusterName,
			"target_repo":      req.TargetRepo,
			"target_path":      req.TargetPath,
		}

		pipelineURL, pipelineID, err := triggerPipeline(token, inputs)
		if err != nil {
			respondError(w, http.StatusBadGateway, err.Error())
			return
		}

		respondJSON(w, http.StatusOK, map[string]any{
			"pipeline_url": pipelineURL,
			"pipeline_id":  pipelineID,
		})
	}
}

// triggerPipeline uses GitLab's standard authenticated pipeline-creation
// endpoint (PRIVATE-TOKEN + JSON inputs), not the older form-encoded
// trigger-token endpoint -- its spec:inputs (JSON) support is the one
// GitLab's own docs give a concrete example for; the trigger-token
// endpoint's inputs support isn't clearly documented. Reuses
// IDP_CLI_PUSH_TOKEN (already api-scoped, group Developer) instead of
// provisioning a separate Pipeline Trigger Token credential.
func triggerPipeline(token string, inputs map[string]any) (string, int, error) {
	body, err := json.Marshal(map[string]any{"inputs": inputs})
	if err != nil {
		return "", 0, err
	}

	apiURL := fmt.Sprintf(
		"https://gitlab.com/api/v4/projects/%s/pipeline?ref=main",
		url.QueryEscape(gitlabProjectPath),
	)

	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, err
	}

	if resp.StatusCode != http.StatusCreated {
		return "", 0, fmt.Errorf("GitLab API returned %s: %s", resp.Status, respBody)
	}

	var result struct {
		ID     int    `json:"id"`
		WebURL string `json:"web_url"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", 0, err
	}

	return result.WebURL, result.ID, nil
}

// publishJobArtifacts maps each of the pipeline's four MR-opening jobs
// (see idp-cli's own .gitlab-ci.yml -- publish-service-mr, publish-
// dashboard-mr, publish-appset-mr, publish-terraform-mr) to the short key
// the frontend uses for its MR link, and to the artifact filename that job
// writes the MR's web_url into. A job that never ran, is still running, or
// ran but had nothing to publish (dashboard=false, target_repo empty,
// ecr_repo_exists=true, or "already exists, nothing new to do") simply has
// no entry in the response -- that's a normal outcome, not an error, so
// handleStatus doesn't distinguish those cases from each other.
var publishJobArtifacts = map[string]string{
	"publish-service-mr":   "service",
	"publish-dashboard-mr": "dashboard",
	"publish-appset-mr":    "appset",
	"publish-terraform-mr": "terraform",
}

type pipelineStatusResponse struct {
	PipelineStatus string            `json:"pipeline_status"`
	Jobs           []jobStatusEntry  `json:"jobs"`
	MRLinks        map[string]string `json:"mr_links"`
}

type jobStatusEntry struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// handleStatus is polled by the frontend after /generate starts a
// pipeline -- GitLab's pipeline API is the source of truth, this server
// holds no state of its own between requests. It exists specifically so
// the tool can show the MRs it opened without the user having to go dig
// through job logs in GitLab's own UI to find them (see the three publish
// jobs' mr-links/*.txt artifacts, written after each job's MR create-or-
// find-existing call).
func handleStatus(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		pipelineID := r.URL.Query().Get("pipeline_id")
		if pipelineID == "" {
			respondError(w, http.StatusBadRequest, "pipeline_id is required")
			return
		}
		if _, err := strconv.Atoi(pipelineID); err != nil {
			respondError(w, http.StatusBadRequest, "pipeline_id must be numeric")
			return
		}

		result, err := fetchPipelineStatus(token, pipelineID)
		if err != nil {
			respondError(w, http.StatusBadGateway, err.Error())
			return
		}

		respondJSON(w, http.StatusOK, result)
	}
}

func fetchPipelineStatus(token, pipelineID string) (*pipelineStatusResponse, error) {
	projectPath := url.QueryEscape(gitlabProjectPath)

	var pipeline struct {
		Status string `json:"status"`
	}
	pipelineURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/pipelines/%s", projectPath, pipelineID)
	if err := gitlabGETJSON(token, pipelineURL, &pipeline); err != nil {
		return nil, fmt.Errorf("fetching pipeline status: %w", err)
	}

	var jobs []struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	jobsURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/pipelines/%s/jobs", projectPath, pipelineID)
	if err := gitlabGETJSON(token, jobsURL, &jobs); err != nil {
		return nil, fmt.Errorf("fetching pipeline jobs: %w", err)
	}

	result := &pipelineStatusResponse{
		PipelineStatus: pipeline.Status,
		MRLinks:        map[string]string{},
	}

	for _, j := range jobs {
		result.Jobs = append(result.Jobs, jobStatusEntry{Name: j.Name, Status: j.Status})

		key, tracked := publishJobArtifacts[j.Name]
		if !tracked || j.Status != "success" {
			continue
		}

		artifactURL := fmt.Sprintf(
			"https://gitlab.com/api/v4/projects/%s/jobs/%d/artifacts/mr-links/%s.txt",
			projectPath, j.ID, key,
		)
		// A 404 here just means that job had nothing to publish (e.g.
		// dashboard=false, or the service already existed) -- not an
		// error, so its absence from mr_links is silent, not surfaced.
		if mrURL, err := gitlabGETText(token, artifactURL); err == nil && mrURL != "" {
			result.MRLinks[key] = mrURL
		}
	}

	return result, nil
}

func gitlabGETJSON(token, apiURL string, out any) error {
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitLab API returned %s: %s", resp.Status, body)
	}
	return json.Unmarshal(body, out)
}

func gitlabGETText(token, apiURL string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("PRIVATE-TOKEN", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitLab API returned %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func respondError(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
