package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	anthropicAPIURL = "https://api.anthropic.com/v1/messages"
	anthropicModel  = "claude-opus-5"
)

// chatRequest/chatResponse are the wire format between the browser and THIS
// server -- not the Anthropic API's own request/response shape (that's
// anthropicRequest/anthropicMessageResponse below). Two separate concerns:
// what the frontend sends us, vs. what we send Anthropic.
type chatRequest struct {
	Message string `json:"message"`
}

type chatResponse struct {
	Summary string          `json:"summary"`
	Params  generateRequest `json:"params"`
}

// Minimal shapes for the Anthropic Messages API -- only the fields this
// server actually reads, same "model just what you need" approach as
// gitlab.go's YAML structs.
type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model        string             `json:"model"`
	MaxTokens    int                `json:"max_tokens"`
	System       string             `json:"system,omitempty"`
	Tools        []anthropicTool    `json:"tools"`
	Messages     []anthropicMessage `json:"messages"`
	OutputConfig map[string]any     `json:"output_config,omitempty"`
}

// A content block can be "text" or "tool_use" -- Type/Text apply to the
// former, ID/Name/Input to the latter. One struct covers both, same as
// gitlab.go's applicationSetElement only models the YAML fields it needs;
// unused fields just stay zero-valued for whichever block type this is.
type anthropicContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type anthropicMessageResponse struct {
	StopReason string                  `json:"stop_reason"`
	Content    []anthropicContentBlock `json:"content"`
}

// The tool Claude is given -- its schema mirrors generateRequest field for
// field (main.go), so json.Unmarshal can decode straight into that type.
var serviceParamsSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"name": map[string]any{
			"type":        "string",
			"description": "Service name, e.g. team-payments-backend",
		},
		"team": map[string]any{
			"type":        "string",
			"description": "Team/namespace this service belongs to, e.g. team-analytics",
		},
		"port": map[string]any{
			"type":        "integer",
			"description": "Container port. Default 8080 if not mentioned.",
		},
		"has_hpa": map[string]any{
			"type":        "boolean",
			"description": "Whether to enable autoscaling (HPA). Default true unless the user says to disable/turn off autoscaling.",
		},
		"replicas": map[string]any{
			"type":        "string",
			"description": "Replica count override, as a string. Empty string if not mentioned.",
		},
		"dockerfile_path": map[string]any{
			"type":        "string",
			"description": "Path to the Dockerfile inside the service's own repo. Default \"Dockerfile\" if not mentioned.",
		},
		"ecr_repo": map[string]any{
			"type":        "string",
			"description": "ECR repository name. Empty string if not mentioned (defaults to the service name).",
		},
		"dashboard": map[string]any{
			"type": "boolean",
			"description": "Whether to generate a starter Grafana dashboard. Default false. " +
				"Only set true when the user clearly asks to CREATE/ADD/GENERATE one " +
				"(e.g. \"add a dashboard\", \"I want a dashboard too\", \"generate a dashboard for it\"). " +
				"Merely mentioning the word \"dashboard\" is NOT a request -- e.g. \"check dashboard\", " +
				"\"look at the dashboard\", or \"is there a dashboard\" are all false, since they ask " +
				"about an existing one, not for a new one to be created.",
		},
		"eks_cluster_name": map[string]any{
			"type":        "string",
			"description": "EKS cluster name. Empty string if not mentioned.",
		},
		"target_repo": map[string]any{
			"type":        "string",
			"description": "Existing GitLab project path (group/repo) to open a Merge Request against, e.g. demo-org/analytics-demo. Empty string if the user didn't name an existing repo.",
		},
		"target_path": map[string]any{
			"type":        "string",
			"description": "Subdirectory inside target_repo to write into, e.g. backend. Empty string if not mentioned.",
		},
	},
	"required": []string{"name", "team"},
}

func handleChat(anthropicKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if anthropicKey == "" {
			respondError(w, http.StatusInternalServerError, "set ANTHROPIC_API_KEY to use /chat")
			return
		}

		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
		if req.Message == "" {
			respondError(w, http.StatusBadRequest, "message is required")
			return
		}

		params, err := extractServiceParams(anthropicKey, req.Message)
		if err != nil {
			respondError(w, http.StatusBadGateway, err.Error())
			return
		}
		params = applyFallbackDefaults(params)

		respondJSON(w, http.StatusOK, chatResponse{
			Summary: summarizeParams(params),
			Params:  params,
		})
	}
}

// extractServiceParams asks Claude to turn a plain-English sentence into
// the same structured fields the /generate endpoint already takes --
// tool-use here is a STRUCTURED EXTRACTION mechanism (one call, no
// tool_result round-trip), not a multi-step agent loop. We never execute
// anything on Claude's say-so; this only returns what it extracted, for
// the human to confirm.
func extractServiceParams(apiKey, message string) (generateRequest, error) {
	reqBody := anthropicRequest{
		Model:     anthropicModel,
		MaxTokens: 1024,
		System: "You extract structured parameters for scaffolding a new " +
			"service onto a Kubernetes platform, from a plain-English " +
			"request. Always call the extract_service_params tool exactly " +
			"once with your best-effort values -- use the defaults named " +
			"in each field's description for anything not mentioned, never " +
			"guess a value the user didn't imply.",
		Tools: []anthropicTool{{
			Name:        "extract_service_params",
			Description: "Record the structured parameters for a new service to scaffold.",
			InputSchema: serviceParamsSchema,
		}},
		Messages:     []anthropicMessage{{Role: "user", Content: message}},
		OutputConfig: map[string]any{"effort": "low"},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return generateRequest{}, err
	}

	req, err := http.NewRequest(http.MethodPost, anthropicAPIURL, bytes.NewReader(body))
	if err != nil {
		return generateRequest{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return generateRequest{}, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return generateRequest{}, err
	}

	if resp.StatusCode != http.StatusOK {
		return generateRequest{}, fmt.Errorf("Anthropic API returned %s: %s", resp.Status, respBody)
	}

	var result anthropicMessageResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return generateRequest{}, err
	}

	for _, block := range result.Content {
		if block.Type == "tool_use" && block.Name == "extract_service_params" {
			var params generateRequest
			if err := json.Unmarshal(block.Input, &params); err != nil {
				return generateRequest{}, fmt.Errorf("parsing extracted params: %w", err)
			}
			return params, nil
		}
	}

	return generateRequest{}, fmt.Errorf("Claude didn't call the extraction tool (stop_reason=%s)", result.StopReason)
}

// Safety net for the fields where an empty/zero value is unambiguous
// ("not set" vs. a real choice) -- has_hpa/dashboard are deliberately NOT
// defaulted here, since Go's bool zero value (false) is indistinguishable
// from "the model explicitly said false"; those two rely on the tool
// description telling Claude the right default itself.
func applyFallbackDefaults(p generateRequest) generateRequest {
	if p.Port == 0 {
		p.Port = 8080
	}
	if p.DockerfilePath == "" {
		p.DockerfilePath = "Dockerfile"
	}
	if p.EKSClusterName == "" {
		p.EKSClusterName = "demoapp-dev-expense-eks"
	}
	return p
}

func summarizeParams(p generateRequest) string {
	hpa := "disabled"
	if p.HasHPA {
		hpa = "enabled"
	}
	dash := "no"
	if p.Dashboard {
		dash = "yes"
	}
	dest := "download only, no merge requests (target_repo not given)"
	if p.TargetRepo != "" {
		dest = fmt.Sprintf("open a Draft MR against %s", p.TargetRepo)
		if p.TargetPath != "" {
			dest += " under " + p.TargetPath + "/"
		}
		// Every scaffold with a target_repo also opens further MRs: always
		// against gitops (registers the service with ArgoCD's Git files
		// generator -- see appset.go) and terraform-modules (provisions its
		// ECR repo -- see terraform.go), unless ecr_repo_exists=true; and,
		// when dashboard=true, one more against observability-dashboards
		// (the starter dashboard). Counted dynamically rather than
		// hardcoding "second"/"third" -- which ordinal each one lands on
		// depends on which of the others are also happening this run, and a
		// user reading only this line would otherwise think just one MR
		// happens, or see a wrong ordinal.
		ordinals := []string{"second", "third", "fourth"}
		extraMRs := []string{"against gitops (registers it with ArgoCD)"}
		if !p.ECRRepoExists {
			extraMRs = append(extraMRs, "against terraform-modules (provisions its ECR repo)")
		}
		if p.Dashboard {
			extraMRs = append(extraMRs, "against observability-dashboards (the starter dashboard)")
		}
		for i, mr := range extraMRs {
			dest += fmt.Sprintf(", plus a %s MR %s", ordinals[i], mr)
		}
	}
	return fmt.Sprintf(
		"Scaffold service %q for team %q on port %d. Autoscaling: %s. Dashboard: %s. Destination: %s.",
		p.Name, p.Team, p.Port, hpa, dash, dest,
	)
}
