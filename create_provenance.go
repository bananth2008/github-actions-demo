package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"
)

const (
	GitHubHostedId = "https://github.com/Attestations/GitHubHostedActions@v1"
	SelfHostedId   = "https://github.com/Attestations/SelfHostedActions@v1"
	TypeId         = "https://github.com/Attestations/GitHubActionsWorkflow@v1"
)

var (
	artifactPath  = flag.String("artifact_path", "", "The file or dir path of the artifacts for which provenance should be generated.")
	outputPath    = flag.String("output_path", "build.provenance", "The path to which the generated provenance should be written.")
	githubContext = flag.String("github_context", "", "The '${github}' context value.")
	runnerContext = flag.String("runner_context", "", "The '${runner}' context value.")
)

type Statement struct {
	Type          string    `json:"_type"`
	Subject       []Subject `json:"subject"`
	PredicateType string    `json:"predicateType"`
	Predicate     `json:"predicate"`
}
type Subject struct {
	Name   string
	Digest DigestSet
}
type Predicate struct {
	Builder   `json:"builder"`
	Metadata  `json:"metadata"`
	Recipe    `json:"recipe"`
	Materials []Item `json:"materials"`
}
type Builder struct {
	Id string `json:"id"`
}
type Metadata struct {
	BuildInvocationId string `json:"buildInvocationId"`
	Completeness      `json:"completeness"`
	Reproducible      bool `json:"reproducible"`
	// BuildStartedOn not defined as it's not available from a GitHub Action.
	BuildFinishedOn string `json:"buildFinishedOn"`
}
type Recipe struct {
	Type              string          `json:"type"`
	DefinedInMaterial int             `json:"definedInMaterial"`
	EntryPoint        string          `json:"entryPoint"`
	Arguments         json.RawMessage `json:"arguments"`
	Environment       AnyContext      `json:"environment"`
}
type Completeness struct {
	Arguments   bool `json:"arguments"`
	Environment bool `json:"environment"`
	Materials   bool `json:"materials"`
}
type DigestSet map[string]string
type Item struct {
	URI    string    `json:"uri"`
	Digest DigestSet `json:"digest"`
}

type AnyContext struct {
	GitHubContext `json:"github"`
	RunnerContext `json:"runner"`
}
type GitHubContext struct {
	Action          string          `json:"action"`
	ActionPath      string          `json:"action_path"`
	Actor           string          `json:"actor"`
	BaseRef         string          `json:"base_ref"`
	Event           json.RawMessage `json:"event"`
	EventName       string          `json:"event_name"`
	EventPath       string          `json:"event_path"`
	HeadRef         string          `json:"head_ref"`
	Job             string          `json:"job"`
	Ref             string          `json:"ref"`
	Repository      string          `json:"repository"`
	RepositoryOwner string          `json:"repository_owner"`
	RunId           string          `json:"run_id"`
	RunNumber       string          `json:"run_number"`
	SHA             string          `json:"sha"`
	Token           string          `json:"token,omitempty"`
	Workflow        string          `json:"workflow"`
	Workspace       string          `json:"workspace"`
}
type RunnerContext struct {
	OS        string `json:"os"`
	Temp      string `json:"temp"`
	ToolCache string `json:"tool_cache"`
}

// See https://docs.github.com/en/actions/reference/events-that-trigger-workflows
// The only Event with dynamically-provided input is workflow_dispatch which
// exposes the user params at the key "input."
type AnyEvent struct {
	Input json.RawMessage `json:"input"`
}

// subjects walks the file or directory at "root" and hashes all files.
func subjects(root string) ([]Subject, error) {
	var s []Subject
	return s, filepath.Walk(root, func(abspath string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relpath, err := filepath.Rel(root, abspath)
		if err != nil {
			return err
		}
		// Note: filepath.Rel() returns "." when "root" and "abspath" point to the same file.
		if relpath == "." {
			relpath = filepath.Base(root)
		}
		contents, err := ioutil.ReadFile(abspath)
		if err != nil {
			return err
		}
		sha := sha256.Sum256(contents)
		shaHex := hex.EncodeToString(sha[:])
		s = append(s, Subject{Name: relpath, Digest: DigestSet{"sha256": shaHex}})
		return nil
	})
}

func parseFlags() {
	flag.Parse()
	if *artifactPath == "" {
		fmt.Println("No value found for required flag: --artifact_path\n")
		flag.Usage()
		os.Exit(1)
	}
	if *outputPath == "" {
		fmt.Println("No value found for required flag: --output_path\n")
		flag.Usage()
		os.Exit(1)
	}
	if *githubContext == "" {
		fmt.Println("No value found for required flag: --github_context\n")
		flag.Usage()
		os.Exit(1)
	}
	if *runnerContext == "" {
		fmt.Println("No value found for required flag: --runner_context\n")
		flag.Usage()
		os.Exit(1)
	}
}

func main() {
	parseFlags()
	stmt := Statement{PredicateType: "https://in-toto.io/provenance/v0.1", Type: "https://in-toto.io/statement/v0.1"}
	subjects, err := subjects(*artifactPath)
	if os.IsNotExist(err) {
		fmt.Println(fmt.Sprintf("Resource path not found: [provided=%s]", *artifactPath))
		os.Exit(1)
	} else if err != nil {
		panic(err)
	}
	stmt.Subject = append(stmt.Subject, subjects...)
	stmt.Predicate = Predicate{
		Builder{},
		Metadata{
			Completeness: Completeness{
				Arguments: true,
				// Environment description is considered fully described by the generated provenance.
				// Context variables are the main dynamic aspect of builds and those are recorded.
				// NOTE: Secrets aren't considered as env inputs in this model and so are omitted.
				Environment: true,
				Materials:   false,
			},
			Reproducible:    false,
			BuildFinishedOn: time.Now().UTC().Format(time.RFC3339),
		},
		Recipe{
			Type:              TypeId,
			DefinedInMaterial: 0,
		},
		[]Item{},
	}

	context := AnyContext{}
	if err := json.Unmarshal([]byte(*githubContext), &context); err != nil {
		panic(err)
	}
	if err := json.Unmarshal([]byte(*runnerContext), &context); err != nil {
		panic(err)
	}
	gh := context.GitHubContext
	token := gh.Token
	fmt.Println(token)
	// Remove access token from the generated provenance.
	context.GitHubContext.Token = ""
	// NOTE: Re-runs are not uniquely identified and can cause run ID collisions.
	stmt.Predicate.Metadata.BuildInvocationId = gh.RunId
	stmt.Predicate.Recipe.EntryPoint = gh.Workflow
	stmt.Predicate.Recipe.Environment = context
	event := AnyEvent{}
	if err := json.Unmarshal(context.GitHubContext.Event, &event); err != nil {
		panic(err)
	}
	stmt.Predicate.Recipe.Arguments = event.Input
	stmt.Predicate.Materials = append(stmt.Predicate.Materials, Item{URI: "https://github.com/" + gh.Repository, Digest: DigestSet{"sha1": gh.SHA}})
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		stmt.Predicate.Builder.Id = GitHubHostedId
	} else {
		stmt.Predicate.Builder.Id = SelfHostedId
	}
	res, _ := json.MarshalIndent(stmt, "  ", "  ")
	fmt.Println(string(res))

	if err := ioutil.WriteFile(*outputPath, res, 0755); err != nil {
		fmt.Println("Failed to write provenance: %s", err)
		os.Exit(1)
	}
}