package revmuximport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

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
	inputs, err := Import(testBinding(t), writeSyntheticPair(t, manifestDoc, findingsDoc), required)
	if err != nil {
		return store.AttachReviewInput{}, err
	}
	return inputs.FreshAttachment()
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

func TestImportRetainsManifestOnlyOptionalDegradation(t *testing.T) {
	manifestDoc, findingsDoc := syntheticPair()
	manifestDoc.Degraded = json.RawMessage(`["optional"]`)
	findingsDoc.Sources.Agents = findingsDoc.Sources.Agents[:1]
	findingsDoc.Sources.Reported = 1
	findingsDoc.OpenQuestions = []reviewItem{}
	input, err := importSynthetic(t, manifestDoc, findingsDoc, []store.ReviewRequirement{{Name: "reviewer", Executor: "pi"}})
	if err != nil {
		t.Fatal(err)
	}
	if !input.Complete || len(input.Sources) != 2 {
		t.Fatalf("optional degradation changed required completeness or disappeared: %+v", input)
	}
	optional := input.Sources[0]
	if optional.Name != "optional" || optional.Executor != "codex" || optional.Required || optional.Responded || !optional.Degraded ||
		optional.Findings != 0 || optional.PreExisting != 0 || optional.OpenQuestions != 0 {
		t.Fatalf("manifest-only optional source was not retained as non-response evidence: %+v", optional)
	}
}

func TestImportRejectsAttributionToManifestOnlyDegradedSource(t *testing.T) {
	for _, kind := range []string{"findings", "pre_existing", "open_questions"} {
		t.Run(kind, func(t *testing.T) {
			manifestDoc, findingsDoc := syntheticPair()
			manifestDoc.Degraded = json.RawMessage(`["optional"]`)
			findingsDoc.Sources.Agents = findingsDoc.Sources.Agents[:1]
			findingsDoc.Sources.Reported = 1
			findingsDoc.Findings = findingsDoc.Findings[:0]
			findingsDoc.PreExisting = findingsDoc.PreExisting[:0]
			findingsDoc.OpenQuestions = findingsDoc.OpenQuestions[:0]
			item := reviewItem{ID: "fictional-roster-only", Sources: []string{"optional"}}
			switch kind {
			case "findings":
				findingsDoc.Findings = []reviewItem{item}
			case "pre_existing":
				findingsDoc.PreExisting = []reviewItem{item}
			case "open_questions":
				findingsDoc.OpenQuestions = []reviewItem{item}
			}
			if _, err := importSynthetic(t, manifestDoc, findingsDoc, []store.ReviewRequirement{{Name: "reviewer", Executor: "pi"}}); err == nil {
				t.Fatal("manifest-only degraded source received item attribution")
			}
		})
	}
}

func TestImportRejectsNullMissingAndDuplicateItemIDs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*findingsDocument)
	}{
		{name: "missing ID", mutate: func(doc *findingsDocument) { doc.Findings[0].ID = "" }},
		{name: "blank ID", mutate: func(doc *findingsDocument) { doc.Findings[0].ID = " \t" }},
		{name: "duplicate finding ID", mutate: func(doc *findingsDocument) {
			doc.Findings = append(doc.Findings, doc.Findings[0])
		}},
		{name: "duplicate ID across item kinds", mutate: func(doc *findingsDocument) {
			doc.OpenQuestions[0].ID = doc.Findings[0].ID
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifestDoc, findingsDoc := syntheticPair()
			test.mutate(&findingsDoc)
			if _, err := importSynthetic(t, manifestDoc, findingsDoc, nil); err == nil {
				t.Fatal("invalid or repeated item ID imported")
			}
		})
	}
	directory := writeDefaultPair(t)
	path := filepath.Join(directory, "findings.json")
	data, err := os.ReadFile(path) // #nosec G304 -- Test-owned synthetic artifact.
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["findings"] = []any{nil}
	data, _ = json.Marshal(document)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(testBinding(t), directory, nil); err == nil {
		t.Fatal("null finding imported as a zero-value row")
	}
}

func TestImportRequiresAttributionForCountedItems(t *testing.T) {
	for _, kind := range []string{"findings", "pre_existing", "open_questions"} {
		for _, state := range []string{"missing", "null", "empty"} {
			t.Run(kind+" "+state, func(t *testing.T) {
				manifestDoc, findingsDoc := syntheticPair()
				findingsDoc.Findings = []reviewItem{}
				findingsDoc.PreExisting = []reviewItem{}
				findingsDoc.OpenQuestions = []reviewItem{}
				item := reviewItem{ID: "fictional-unattributed", Sources: nil}
				if state == "empty" {
					item.Sources = []string{}
				}
				switch kind {
				case "findings":
					findingsDoc.Findings = []reviewItem{item}
				case "pre_existing":
					findingsDoc.PreExisting = []reviewItem{item}
				case "open_questions":
					findingsDoc.OpenQuestions = []reviewItem{item}
				}
				directory := writeSyntheticPair(t, manifestDoc, findingsDoc)
				if state == "missing" {
					path := filepath.Join(directory, "findings.json")
					data, err := os.ReadFile(path) // #nosec G304 -- Test-owned synthetic artifact.
					if err != nil {
						t.Fatal(err)
					}
					var document map[string]any
					if err := json.Unmarshal(data, &document); err != nil {
						t.Fatal(err)
					}
					delete(document[kind].([]any)[0].(map[string]any), "sources")
					data, _ = json.Marshal(document)
					if err := os.WriteFile(path, data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				inputs, err := Import(testBinding(t), directory, []store.ReviewRequirement{{Name: "reviewer", Executor: "pi"}})
				if err != nil || inputs.LegacyReplay == nil {
					t.Fatalf("old payload unavailable for receipt lookup: %v", err)
				}
				if _, err := inputs.FreshAttachment(); err == nil {
					t.Fatal("unattributed counted item eligible for a fresh attachment")
				}
			})
		}
	}
	manifestDoc, findingsDoc := syntheticPair()
	findingsDoc.Immaterial = []reviewItem{{ID: "fictional-immaterial", Sources: nil}}
	if _, err := importSynthetic(t, manifestDoc, findingsDoc, []store.ReviewRequirement{{Name: "reviewer", Executor: "pi"}}); err != nil {
		t.Fatalf("uncounted immaterial item without attribution rejected: %v", err)
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

func TestArtifactsRejectDuplicateAndCaseAliasedKeys(t *testing.T) {
	tests := []struct {
		name, file, old, replacement string
	}{
		{name: "duplicate manifest identity", file: "manifest.json", old: `"run":"01-review"`, replacement: `"run":"other","run":"01-review"`},
		{name: "case alias manifest identity", file: "manifest.json", old: `"task":"fictional-task"`, replacement: `"Task":"fictional-task"`},
		{name: "duplicate nested identity", file: "findings.json", old: `"run":"01-review"`, replacement: `"run":"other","run":"01-review"`},
		{name: "case alias nested identity", file: "findings.json", old: `"task":"fictional-task"`, replacement: `"Task":"fictional-task"`},
		{name: "case alias source role", file: "findings.json", old: `"executor":"pi"`, replacement: `"Executor":"pi"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := writeDefaultPair(t)
			path := filepath.Join(directory, test.file)
			data, err := os.ReadFile(path) // #nosec G304 -- Test-owned synthetic artifact.
			if err != nil {
				t.Fatal(err)
			}
			changed := strings.Replace(string(data), test.old, test.replacement, 1)
			if changed == string(data) {
				t.Fatal("fixture key not found")
			}
			if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Import(testBinding(t), directory, nil); err == nil {
				t.Fatal("duplicate or case-aliased artifact key imported")
			}
		})
	}
	binding := testBinding(t)
	data, _ := json.Marshal(binding)
	for _, changed := range []string{
		strings.Replace(string(data), `"run":"01-review"`, `"run":"other","run":"01-review"`, 1),
		strings.Replace(string(data), `"run":"01-review"`, `"Run":"01-review"`, 1),
	} {
		path := filepath.Join(t.TempDir(), "binding.json")
		if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadBinding(path); err == nil {
			t.Fatal("duplicate or case-aliased binding key accepted")
		}
	}
}

func TestRejectDuplicateKeysBoundsNestingBeforeRecursion(t *testing.T) {
	data := []byte(strings.Repeat("[", maxArtifactNesting+1) + strings.Repeat("]", maxArtifactNesting+1))
	if err := rejectDuplicateKeys(data); err == nil || !strings.Contains(err.Error(), "invalid or duplicate") {
		t.Fatalf("excessive JSON nesting was not rejected: %v", err)
	}
	valid := []byte(strings.Repeat("[", maxArtifactNesting) + strings.Repeat("]", maxArtifactNesting))
	if err := rejectDuplicateKeys(valid); err != nil {
		t.Fatalf("nesting at the explicit bound rejected: %v", err)
	}
}

func TestArtifactFIFORejectsWithoutBlocking(t *testing.T) {
	directory := writeDefaultPair(t)
	path := filepath.Join(directory, "manifest.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := Import(testBinding(t), directory, nil)
		finished <- err
	}()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("FIFO artifact imported")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FIFO artifact blocked import")
	}
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

func TestBindingDestinationPreflightRejectsUnsafeParent(t *testing.T) {
	binding := testBinding(t)
	root := t.TempDir()
	for _, mode := range []os.FileMode{0o755, 0o700} {
		parent := filepath.Join(root, fmt.Sprintf("parent-%o", mode))
		if err := os.Mkdir(parent, mode); err != nil {
			t.Fatal(err)
		}
		if mode == 0o700 {
			// #nosec G302 -- Deliberately unsafe directory verifies preflight rejection.
			if err := os.Chmod(parent, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		path := filepath.Join(parent, "binding.json")
		if err := ValidateBindingDestination(path, binding); err == nil {
			t.Fatalf("binding in mode-%o parent passed preflight", 0o755)
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("preflight created binding in unsafe parent: %v", err)
		}
	}
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBindingDestination(filepath.Join(link, "binding.json"), binding); err == nil {
		t.Fatal("symlink binding parent passed preflight")
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

func TestImportRequiredSourceLimitKeepsValidIncompleteReviewAttachable(t *testing.T) {
	required := make([]store.ReviewRequirement, store.MaxRequiredSources)
	for index := range required {
		required[index] = store.ReviewRequirement{Name: fmt.Sprintf("missing-%02d", index), Executor: "codex"}
	}
	manifestDoc, findingsDoc := syntheticPair()
	input, err := importSynthetic(t, manifestDoc, findingsDoc, required)
	if err != nil {
		t.Fatal(err)
	}
	if input.Complete || len(input.RequiredSources) != store.MaxRequiredSources || len(input.IncompleteReasons) != store.MaxRequiredSources {
		t.Fatalf("32-source incomplete review not preserved: %+v", input)
	}
	required = append(required, store.ReviewRequirement{Name: "one-too-many", Executor: "codex"})
	if _, err := importSynthetic(t, manifestDoc, findingsDoc, required); err == nil {
		t.Fatal("33 required sources exceeded the aligned API limit")
	}
}
