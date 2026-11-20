package revmuximport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lllypuk/artel/internal/store"
)

const (
	testBase = "1111111111111111111111111111111111111111"
	testHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func boolPointer(value bool) *bool { return &value }

func testAgent(name, executor string) agent {
	return agent{Name: name, Executor: executor, Lenses: []string{"bugs"}, Degraded: boolPointer(false)}
}

func syntheticPair() (manifest, findingsDocument) {
	manifestDoc := manifest{
		Task: "fictional-task", Run: "01-review", Profile: "pi-codex",
		FinishedAt: "2026-11-20T12:00:00Z", Agents: []agent{testAgent("reviewer", "pi"), testAgent("optional", "codex")},
		Degraded: json.RawMessage("null"), Prompts: []json.RawMessage{}, Stages: []json.RawMessage{},
	}
	findingsDoc := findingsDocument{
		Scope: scope{Task: "fictional-task", Run: "01-review"},
		Sources: sourceList{Expected: 2, Reported: 2, Degraded: []string{}, Agents: []agent{
			testAgent("reviewer", "pi"), testAgent("optional", "codex"),
		}},
		Findings:      []reviewItem{{ID: "fictional-1", Title: "Synthetic finding", Body: "not exported", Sources: []string{"reviewer"}, Lenses: []string{"bugs"}}},
		PreExisting:   []reviewItem{{ID: "fictional-old", Sources: []string{"reviewer"}}},
		OpenQuestions: []reviewItem{{ID: "fictional-question", Sources: []string{"optional"}}},
		Immaterial:    []reviewItem{},
		Stats:         stats{FinishedAt: manifestDoc.FinishedAt, Stages: []json.RawMessage{}},
	}
	return manifestDoc, findingsDoc
}

func testBinding(t *testing.T) Binding {
	t.Helper()
	return Binding{Version: bindingVersion, Repo: filepath.Join(t.TempDir(), ".git"),
		RevmuxTask: "fictional-task", Run: "01-review", Base: testBase, Head: testHead, RangeSource: "operator"}
}

func writeSyntheticPair(t *testing.T, manifestDoc manifest, findingsDoc findingsDocument) string {
	t.Helper()
	directory := t.TempDir()
	for name, value := range map[string]any{"manifest.json": manifestDoc, "findings.json": findingsDoc} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

func importSynthetic(t *testing.T, manifestDoc manifest, findingsDoc findingsDocument, required []store.ReviewRequirement) (store.AttachReviewInput, error) {
	t.Helper()
	return Import(testBinding(t), writeSyntheticPair(t, manifestDoc, findingsDoc), required)
}

func writeDefaultPair(t *testing.T) string {
	t.Helper()
	manifestDoc, findingsDoc := syntheticPair()
	return writeSyntheticPair(t, manifestDoc, findingsDoc)
}

func assertNormalizedSummary(t *testing.T, input store.AttachReviewInput) {
	t.Helper()
	if !input.Complete || input.RangeSource != "operator" || input.RevmuxTask != "fictional-task" || input.Run != "01-review" ||
		input.Findings != 1 || input.PreExisting != 1 || input.OpenQuestions != 1 || len(input.IncompleteReasons) != 0 {
		t.Fatalf("unexpected normalized review: %+v", input)
	}
}

func assertNormalizedSources(t *testing.T, input store.AttachReviewInput) {
	t.Helper()
	if len(input.Sources) != 2 || input.Sources[0].Name != "optional" || input.Sources[1].Name != "reviewer" {
		t.Fatalf("sources not normalized: %+v", input.Sources)
	}
	reviewer := input.Sources[1]
	if !reviewer.Required || !reviewer.Responded || reviewer.Degraded || reviewer.Findings != 1 || reviewer.PreExisting != 1 {
		t.Fatalf("unexpected reviewer evidence: %+v", reviewer)
	}
	if input.Sources[0].OpenQuestions != 1 {
		t.Fatalf("open question not attributed: %+v", input.Sources[0])
	}
}

func assertRawFindingsExcluded(t *testing.T, input store.AttachReviewInput) {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "Synthetic finding") || strings.Contains(string(encoded), "not exported") {
		t.Fatal("raw finding text entered normalized payload")
	}
}

func TestImportNormalizedFindingsAndOpenQuestions(t *testing.T) {
	manifestDoc, findingsDoc := syntheticPair()
	input, err := importSynthetic(t, manifestDoc, findingsDoc, []store.ReviewRequirement{{Name: "reviewer", Executor: "pi"}})
	if err != nil {
		t.Fatal(err)
	}
	assertNormalizedSummary(t, input)
	assertNormalizedSources(t, input)
	assertRawFindingsExcluded(t, input)
}

func TestImportZeroFindingsStillRespondedAndQuestionsRemain(t *testing.T) {
	manifestDoc, findingsDoc := syntheticPair()
	findingsDoc.Findings = []reviewItem{}
	input, err := importSynthetic(t, manifestDoc, findingsDoc, []store.ReviewRequirement{{Name: "reviewer", Executor: "pi"}})
	if err != nil {
		t.Fatal(err)
	}
	if !input.Complete || input.Findings != 0 || input.OpenQuestions != 1 || !input.Sources[1].Responded {
		t.Fatalf("zero findings lost response or questions: %+v", input)
	}
}

func TestImportCompletenessUsesRequiredPairs(t *testing.T) {
	tests := []struct {
		name         string
		required     []store.ReviewRequirement
		mutate       func(*manifest, *findingsDocument)
		wantComplete bool
		wantReason   string
		wantSource   string
	}{
		{name: "empty requirements", wantReason: "required source list is empty"},
		{name: "profile does not substitute executor", required: []store.ReviewRequirement{{Name: "reviewer", Executor: "codex"}}, wantReason: "required source missing: reviewer (codex)"},
		{name: "required source absent", required: []store.ReviewRequirement{{Name: "reviewer", Executor: "pi"}}, wantReason: "required source missing: reviewer (pi)", mutate: func(_ *manifest, findings *findingsDocument) {
			findings.Sources.Agents = findings.Sources.Agents[1:]
			findings.Sources.Reported = 1
			findings.Findings, findings.PreExisting = []reviewItem{}, []reviewItem{}
		}},
		{name: "degraded only findings list", required: []store.ReviewRequirement{{Name: "reviewer", Executor: "pi"}}, wantReason: "required source degraded: reviewer (pi)", wantSource: "reviewer", mutate: func(_ *manifest, findings *findingsDocument) {
			findings.Sources.Degraded = []string{"reviewer"}
			findings.Sources.Reported = 1
		}},
		{name: "degraded only findings flag", required: []store.ReviewRequirement{{Name: "reviewer", Executor: "pi"}}, wantReason: "required source degraded: reviewer (pi)", wantSource: "reviewer", mutate: func(_ *manifest, findings *findingsDocument) {
			findings.Sources.Agents[0].Degraded = boolPointer(true)
			findings.Sources.Reported = 1
		}},
		{name: "degraded only manifest list", required: []store.ReviewRequirement{{Name: "reviewer", Executor: "pi"}}, wantReason: "required source degraded: reviewer (pi)", wantSource: "reviewer", mutate: func(manifest *manifest, findings *findingsDocument) {
			manifest.Degraded = json.RawMessage(`["reviewer"]`)
			findings.Sources.Reported = 1
		}},
		{name: "optional degradation remains separate", required: []store.ReviewRequirement{{Name: "reviewer", Executor: "pi"}}, wantComplete: true, wantSource: "optional", mutate: func(_ *manifest, findings *findingsDocument) {
			findings.Sources.Agents[1].Degraded = boolPointer(true)
			findings.Sources.Reported = 1
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifestDoc, findingsDoc := syntheticPair()
			if test.mutate != nil {
				test.mutate(&manifestDoc, &findingsDoc)
			}
			input, err := importSynthetic(t, manifestDoc, findingsDoc, test.required)
			if err != nil {
				t.Fatal(err)
			}
			if input.Complete != test.wantComplete {
				t.Fatalf("complete=%v want %v: %+v", input.Complete, test.wantComplete, input)
			}
			if test.wantReason != "" && (len(input.IncompleteReasons) != 1 || input.IncompleteReasons[0] != test.wantReason) {
				t.Fatalf("reasons=%v want %q", input.IncompleteReasons, test.wantReason)
			}
			if test.wantSource != "" {
				for _, source := range input.Sources {
					if source.Name == test.wantSource {
						if test.wantComplete && source.Degraded != (source.Name == "optional") {
							t.Fatalf("optional degradation misrepresented: %+v", source)
						}
						if !test.wantComplete && !source.Degraded {
							t.Fatalf("required degradation missing: %+v", source)
						}
					}
				}
			}
		})
	}
}

func TestImportRejectsBrokenOrInconsistentPair(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*manifest, *findingsDocument)
	}{
		{name: "unfinished", mutate: func(manifest *manifest, _ *findingsDocument) { manifest.FinishedAt = "" }},
		{name: "completion mismatch", mutate: func(_ *manifest, findings *findingsDocument) { findings.Stats.FinishedAt = "2026-11-20T12:00:01Z" }},
		{name: "manifest task mismatch", mutate: func(manifest *manifest, _ *findingsDocument) { manifest.Task = "other-task" }},
		{name: "manifest run mismatch", mutate: func(manifest *manifest, _ *findingsDocument) { manifest.Run = "other-run" }},
		{name: "scope task mismatch", mutate: func(_ *manifest, findings *findingsDocument) { findings.Scope.Task = "other-task" }},
		{name: "scope run mismatch", mutate: func(_ *manifest, findings *findingsDocument) { findings.Scope.Run = "other-run" }},
		{name: "role contradiction", mutate: func(_ *manifest, findings *findingsDocument) { findings.Sources.Agents[0].Executor = "codex" }},
		{name: "duplicate reported source", mutate: func(_ *manifest, findings *findingsDocument) {
			findings.Sources.Agents = append(findings.Sources.Agents, testAgent("reviewer", "pi"))
		}},
		{name: "duplicate degraded source", mutate: func(_ *manifest, findings *findingsDocument) {
			findings.Sources.Degraded = []string{"reviewer", "reviewer"}
		}},
		{name: "unknown degraded source", mutate: func(_ *manifest, findings *findingsDocument) { findings.Sources.Degraded = []string{"unknown"} }},
		{name: "expected count mismatch", mutate: func(_ *manifest, findings *findingsDocument) { findings.Sources.Expected = 1 }},
		{name: "reported count mismatch", mutate: func(_ *manifest, findings *findingsDocument) { findings.Sources.Reported = 1 }},
		{name: "unknown finding source", mutate: func(_ *manifest, findings *findingsDocument) { findings.Findings[0].Sources = []string{"unknown"} }},
		{name: "duplicate finding source", mutate: func(_ *manifest, findings *findingsDocument) {
			findings.Findings[0].Sources = []string{"reviewer", "reviewer"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifestDoc, findingsDoc := syntheticPair()
			test.mutate(&manifestDoc, &findingsDoc)
			if _, err := importSynthetic(t, manifestDoc, findingsDoc, []store.ReviewRequirement{{Name: "reviewer", Executor: "pi"}}); err == nil {
				t.Fatal("inconsistent pair imported")
			}
		})
	}
}

func TestImportRejectsMalformedUnsupportedAndOversizedArtifacts(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		directory := writeDefaultPair(t)
		if err := os.WriteFile(filepath.Join(directory, "manifest.json"), []byte(`{"task":`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Import(testBinding(t), directory, nil); err == nil {
			t.Fatal("malformed JSON imported")
		}
	})
	t.Run("unknown field", func(t *testing.T) {
		directory := writeDefaultPair(t)
		// #nosec G304 -- Reads only this test's synthetic temporary artifact.
		data, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if json.Unmarshal(data, &object) != nil {
			t.Fatal("invalid fixture")
		}
		object["execute"] = "do-not-run"
		data, _ = json.Marshal(object)
		if err := os.WriteFile(filepath.Join(directory, "manifest.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Import(testBinding(t), directory, nil); err == nil {
			t.Fatal("unsupported field imported")
		}
	})
	t.Run("too large", func(t *testing.T) {
		directory := writeDefaultPair(t)
		if err := os.WriteFile(filepath.Join(directory, "findings.json"), make([]byte, MaxArtifactSize+1), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Import(testBinding(t), directory, nil); err == nil {
			t.Fatal("oversized artifact imported")
		}
	})
}

func TestBindingReplayAndValidation(t *testing.T) {
	binding := testBinding(t)
	path := filepath.Join(t.TempDir(), "private", "binding.json")
	if err := SaveBinding(path, binding); err != nil {
		t.Fatal(err)
	}
	if err := SaveBinding(path, binding); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadBinding(path)
	if err != nil || loaded != binding {
		t.Fatalf("binding=%+v err=%v", loaded, err)
	}
	changed := binding
	changed.Run = "different"
	if err := SaveBinding(path, changed); err == nil {
		t.Fatal("different binding replaced original")
	}
	loaded, err = LoadBinding(path)
	if err != nil || loaded != binding {
		t.Fatal("original binding changed")
	}
	for _, invalid := range []Binding{
		{Version: 2, Repo: binding.Repo, RevmuxTask: binding.RevmuxTask, Run: binding.Run, Base: binding.Base, Head: binding.Head, RangeSource: "operator"},
		{Version: 1, Repo: "relative", RevmuxTask: binding.RevmuxTask, Run: binding.Run, Base: binding.Base, Head: binding.Head, RangeSource: "operator"},
		{Version: 1, Repo: binding.Repo, RevmuxTask: binding.RevmuxTask, Run: binding.Run, Base: "HEAD", Head: binding.Head, RangeSource: "operator"},
		{Version: 1, Repo: binding.Repo, RevmuxTask: binding.RevmuxTask, Run: binding.Run, Base: binding.Base, Head: binding.Head, RangeSource: "artifact"},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("invalid binding accepted: %+v", invalid)
		}
	}
}

func TestImportRejectsDuplicateRequirements(t *testing.T) {
	manifestDoc, findingsDoc := syntheticPair()
	_, err := importSynthetic(t, manifestDoc, findingsDoc, []store.ReviewRequirement{
		{Name: "reviewer", Executor: "pi"}, {Name: "reviewer", Executor: "pi"},
	})
	if err == nil {
		t.Fatal("duplicate requirement accepted")
	}
}
