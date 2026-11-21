// Package revmuximport reads the local revmux manifest/findings pair and
// produces only the bounded, normalized evidence accepted by the owner API.
package revmuximport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/lllypuk/artel/internal/local"
	"github.com/lllypuk/artel/internal/store"
)

const (
	// MaxArtifactSize bounds each independently read report file. Raw findings
	// are never copied into the owner request or response.
	MaxArtifactSize    = 4 * 1024 * 1024
	maxArtifactNesting = 100
	bindingVersion     = 1
)

// Binding is the owner's pre-run assertion. Base and Head are literal commits,
// not names resolved from a report or from the worktree's current HEAD.
type Binding struct {
	Version     int    `json:"version"`
	Repo        string `json:"repo"`
	RevmuxTask  string `json:"revmux_task"`
	Run         string `json:"run"`
	Base        string `json:"base"`
	Head        string `json:"head"`
	RangeSource string `json:"range_source"`
}

func validText(value string, max int, required bool) bool {
	return utf8.ValidString(value) && len(value) <= max && !strings.ContainsRune(value, 0) && (!required || strings.TrimSpace(value) != "")
}

func validSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Validate rejects a binding that cannot be compared exactly with a submitted
// result. The CLI separately proves Repo is a canonical Git common-dir.
func (b Binding) Validate() error {
	if b.Version != bindingVersion || !validText(b.Repo, 4096, true) ||
		!filepath.IsAbs(b.Repo) || filepath.Clean(b.Repo) != b.Repo ||
		!validText(b.RevmuxTask, 200, true) || !validText(b.Run, 200, true) ||
		!validSHA(b.Base) || !validSHA(b.Head) || b.RangeSource != "operator" {
		return errors.New("invalid revmux binding")
	}
	return nil
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var scan func(int) error
	scan = func(depth int) error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		if delimiter != '{' && delimiter != '[' {
			return errors.New("unexpected JSON delimiter")
		}
		if depth >= maxArtifactNesting {
			return errors.New("JSON nesting limit exceeded")
		}
		switch delimiter {
		case '{':
			seen := make(map[string]bool)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return errors.New("duplicate JSON key")
				}
				seen[key] = true
				if err := scan(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := scan(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("unexpected JSON delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	if err := scan(0); err != nil {
		return errors.New("invalid or duplicate JSON object keys")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON value")
	}
	return nil
}

// validateJSONKeys applies struct tags exactly at every consumed object. The
// standard decoder otherwise accepts case aliases such as Task for task.
func validateJSONKeys(data []byte, target reflect.Type) error {
	if target.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return nil
		}
		return validateJSONKeys(data, target.Elem())
	}
	if target == reflect.TypeOf(json.RawMessage{}) {
		return nil
	}
	switch target.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
			return errors.New("invalid JSON object")
		}
		known := make(map[string]reflect.Type, target.NumField())
		for index := 0; index < target.NumField(); index++ {
			field := target.Field(index)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if field.PkgPath == "" && name != "" && name != "-" {
				known[name] = field.Type
			}
		}
		for name, value := range fields {
			field, ok := known[name]
			if !ok {
				return errors.New("unsupported or case-aliased JSON key")
			}
			if err := validateJSONKeys(value, field); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return nil
		}
		var values []json.RawMessage
		if err := json.Unmarshal(data, &values); err != nil {
			return errors.New("invalid JSON array")
		}
		for _, value := range values {
			if err := validateJSONKeys(value, target.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

func decodeStrict(data []byte, target any, required ...string) error {
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	if err := validateJSONKeys(data, reflect.TypeOf(target)); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return errors.New("invalid JSON object")
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return fmt.Errorf("required JSON field %s missing", name)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("unsupported JSON shape")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON value")
	}
	return nil
}

// LoadBinding reads the private local pre-run record, never a report field.
func LoadBinding(path string) (Binding, error) {
	var binding Binding
	data, err := local.ReadPrivateFile(path)
	if err != nil {
		return binding, errors.New("revmux binding unavailable or unsafe")
	}
	if err := decodeStrict(data, &binding, "version", "repo", "revmux_task", "run", "base", "head", "range_source"); err != nil {
		return Binding{}, errors.New("invalid revmux binding")
	}
	return binding, binding.Validate()
}

// ValidateBindingDestination rejects an unsafe or conflicting existing record
// before the CLI commits a submission. The final SaveBinding still rechecks the
// destination to close the check/write race as far as the filesystem permits.
func ValidateBindingDestination(path string, binding Binding) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if err := local.ValidatePrivateFileDestination(path); err != nil {
			return errors.New("revmux binding destination unavailable or unsafe")
		}
		return nil
	} else if err != nil {
		return errors.New("revmux binding unavailable or unsafe")
	}
	existing, err := LoadBinding(path)
	if err != nil || !reflect.DeepEqual(existing, binding) {
		return errors.New("revmux binding exists with different content")
	}
	return nil
}

// SaveBinding publishes a binding once. An identical record is a safe command
// replay; different content is never silently replaced.
func SaveBinding(path string, binding Binding) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if err := local.WritePrivateFile(path, data, false); err != nil {
			return errors.New("cannot persist revmux binding")
		}
		return nil
	} else if err != nil {
		return errors.New("revmux binding unavailable or unsafe")
	}
	existing, err := LoadBinding(path)
	if err != nil || !reflect.DeepEqual(existing, binding) {
		return errors.New("revmux binding exists with different content")
	}
	return nil
}

// The supported artifact form mirrors revmux's manifest task/run/finished_at/
// agents/degraded fields and findings scope/sources/findings/pre_existing/
// open_questions/stats fields. Other manifest metadata is accepted but not
// imported. Item text is decoded only to validate the known shape and count
// source references; it is never included in AttachReviewInput.
type manifest struct {
	Task       string            `json:"task"`
	Run        string            `json:"run"`
	Profile    string            `json:"profile"`
	ScopePath  string            `json:"scope_path"`
	StartedAt  string            `json:"started_at"`
	FinishedAt string            `json:"finished_at"`
	DurationMS int64             `json:"duration_ms"`
	Tokens     int64             `json:"tokens"`
	Agents     []agent           `json:"agents"`
	Degraded   json.RawMessage   `json:"degraded"`
	Prompts    []json.RawMessage `json:"prompts"`
	Stages     []json.RawMessage `json:"stages"`
}

type agent struct {
	Name           string   `json:"name"`
	Lenses         []string `json:"lenses"`
	Executor       string   `json:"executor"`
	RequestedModel string   `json:"requested_model"`
	ActualModel    string   `json:"actual_model"`
	Effort         string   `json:"effort"`
	Tokens         int64    `json:"tokens"`
	Raised         int      `json:"raised"`
	Degraded       *bool    `json:"degraded"`
}

type findingsDocument struct {
	Scope         scope        `json:"scope"`
	Sources       sourceList   `json:"sources"`
	Findings      []reviewItem `json:"findings"`
	OpenQuestions []reviewItem `json:"open_questions"`
	PreExisting   []reviewItem `json:"pre_existing"`
	Immaterial    []reviewItem `json:"immaterial"`
	Stats         stats        `json:"stats"`
}

type scope struct {
	Task      string `json:"task"`
	Run       string `json:"run"`
	ScopePath string `json:"scope_path"`
}

type sourceList struct {
	Expected int      `json:"expected"`
	Reported int      `json:"reported"`
	Degraded []string `json:"degraded"`
	Agents   []agent  `json:"agents"`
}

type reviewItem struct {
	ID         string   `json:"id"`
	File       string   `json:"file"`
	Line       int      `json:"line"`
	EndLine    int      `json:"end_line"`
	Severity   string   `json:"severity"`
	Confidence int      `json:"confidence"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Fix        string   `json:"fix"`
	Sources    []string `json:"sources"`
	Lenses     []string `json:"lenses"`
	Verdict    string   `json:"verdict"`
}

type stats struct {
	StartedAt  string            `json:"started_at"`
	FinishedAt string            `json:"finished_at"`
	DurationMS int64             `json:"duration_ms"`
	Tokens     int64             `json:"tokens"`
	Stages     []json.RawMessage `json:"stages"`
}

func readArtifact(path string, target any, required ...string) error {
	// O_NONBLOCK makes a FIFO rejectable by fstat instead of hanging in open.
	// O_NOFOLLOW also keeps the validated report directory from redirecting a
	// consumed artifact through a final-component symlink.
	// #nosec G304 -- The owner explicitly selects a local report directory; URLs are never opened.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("revmux artifact unavailable")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxArtifactSize {
		return errors.New("revmux artifact is not a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxArtifactSize+1))
	if err != nil || len(data) > MaxArtifactSize {
		return errors.New("revmux artifact exceeds size limit")
	}
	if !utf8.Valid(data) {
		return errors.New("revmux artifact is not UTF-8")
	}
	return decodeStrict(data, target, required...)
}

func sourceKey(name, executor string) string { return name + "\x00" + executor }

func validateAgent(value agent) error {
	if !validText(value.Name, 200, true) || !validText(value.Executor, 120, true) || value.Degraded == nil {
		return errors.New("invalid revmux source")
	}
	return nil
}

func indexAgents(values []agent, what string) (map[string]agent, error) {
	byName := make(map[string]agent, len(values))
	for _, value := range values {
		if err := validateAgent(value); err != nil {
			return nil, err
		}
		if _, exists := byName[value.Name]; exists {
			return nil, fmt.Errorf("duplicate or ambiguous %s source name", what)
		}
		byName[value.Name] = value
	}
	return byName, nil
}

func degradedSet(raw json.RawMessage) (map[string]bool, error) {
	set := make(map[string]bool)
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return set, nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, errors.New("invalid manifest degradation list")
	}
	for _, name := range names {
		if !validText(name, 200, true) || set[name] {
			return nil, errors.New("duplicate or invalid degraded source")
		}
		set[name] = true
	}
	return set, nil
}

func listedDegraded(names []string) (map[string]bool, error) {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		if !validText(name, 200, true) || set[name] {
			return nil, errors.New("duplicate or invalid degraded source")
		}
		set[name] = true
	}
	return set, nil
}

func normalizeRequirements(required []store.ReviewRequirement) ([]store.ReviewRequirement, map[string]bool, error) {
	if len(required) > store.MaxRequiredSources {
		return nil, nil, errors.New("too many required sources")
	}
	values := append([]store.ReviewRequirement{}, required...)
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		key := sourceKey(value.Name, value.Executor)
		if !validText(value.Name, 200, true) || !validText(value.Executor, 120, true) || seen[key] {
			return nil, nil, errors.New("invalid or duplicate required source")
		}
		seen[key] = true
	}
	slices.SortFunc(values, func(a, b store.ReviewRequirement) int {
		if compared := strings.Compare(a.Name, b.Name); compared != 0 {
			return compared
		}
		return strings.Compare(a.Executor, b.Executor)
	})
	return values, seen, nil
}

func readPair(binding Binding, reportDir string) (manifest, findingsDocument, error) {
	var manifestDoc manifest
	var findingsDoc findingsDocument
	if !filepath.IsAbs(reportDir) || filepath.Clean(reportDir) != reportDir {
		return manifestDoc, findingsDoc, errors.New("canonical absolute report directory required")
	}
	if err := readArtifact(filepath.Join(reportDir, "manifest.json"), &manifestDoc,
		"task", "run", "finished_at", "agents", "degraded"); err != nil {
		return manifestDoc, findingsDoc, err
	}
	if err := readArtifact(filepath.Join(reportDir, "findings.json"), &findingsDoc,
		"scope", "sources", "findings", "pre_existing", "open_questions", "stats"); err != nil {
		return manifestDoc, findingsDoc, err
	}
	if manifestDoc.FinishedAt == "" || manifestDoc.Agents == nil || findingsDoc.Findings == nil ||
		findingsDoc.PreExisting == nil || findingsDoc.OpenQuestions == nil || findingsDoc.Sources.Agents == nil ||
		findingsDoc.Sources.Degraded == nil {
		return manifestDoc, findingsDoc, errors.New("unfinished or unsupported revmux artifact pair")
	}
	if manifestDoc.Task != binding.RevmuxTask || manifestDoc.Run != binding.Run ||
		findingsDoc.Scope.Task != manifestDoc.Task || findingsDoc.Scope.Run != manifestDoc.Run {
		return manifestDoc, findingsDoc, errors.New("revmux task/run mismatch")
	}
	if findingsDoc.Stats.FinishedAt != "" && findingsDoc.Stats.FinishedAt != manifestDoc.FinishedAt {
		return manifestDoc, findingsDoc, errors.New("revmux completion metadata mismatch")
	}
	if err := validateReviewItemIDs(findingsDoc); err != nil {
		return manifestDoc, findingsDoc, err
	}
	return manifestDoc, findingsDoc, nil
}

func validateReviewItemIDs(findingsDoc findingsDocument) error {
	seen := make(map[string]bool)
	for _, items := range [][]reviewItem{findingsDoc.Findings, findingsDoc.PreExisting, findingsDoc.OpenQuestions, findingsDoc.Immaterial} {
		for _, item := range items {
			if !validText(item.ID, 200, true) || seen[item.ID] {
				return errors.New("finding item has a missing, null or duplicate ID")
			}
			seen[item.ID] = true
		}
	}
	return nil
}

func mapSources(manifestDoc manifest, findingsDoc findingsDocument) (map[string]agent, map[string]agent, map[string]bool, error) {
	roster, err := indexAgents(manifestDoc.Agents, "manifest")
	if err != nil {
		return nil, nil, nil, err
	}
	actual, err := indexAgents(findingsDoc.Sources.Agents, "reported")
	if err != nil {
		return nil, nil, nil, err
	}
	manifestDegraded, err := degradedSet(manifestDoc.Degraded)
	if err != nil {
		return nil, nil, nil, err
	}
	findingsDegraded, err := listedDegraded(findingsDoc.Sources.Degraded)
	if err != nil {
		return nil, nil, nil, err
	}
	for name, reported := range actual {
		expected, ok := roster[name]
		if !ok || expected.Executor != reported.Executor {
			return nil, nil, nil, errors.New("contradictory source role in revmux pair")
		}
	}
	for name := range manifestDegraded {
		if _, ok := roster[name]; !ok {
			return nil, nil, nil, errors.New("manifest degradation names an unknown source")
		}
	}
	for name := range findingsDegraded {
		if _, ok := actual[name]; !ok {
			return nil, nil, nil, errors.New("findings degradation names an unreported source")
		}
	}
	degraded := make(map[string]bool, len(roster))
	for name, expected := range roster {
		reported, ok := actual[name]
		reportedDegraded := ok && *reported.Degraded
		degraded[name] = *expected.Degraded || manifestDegraded[name] || reportedDegraded || findingsDegraded[name]
	}
	return roster, actual, degraded, nil
}

func countItems(items []reviewItem, sources []store.ReviewSource, indexes map[string]int, selectCount func(*store.ReviewSource) *int) error {
	for _, item := range items {
		// Counted items without attribution would inflate only the overall total.
		// Immaterial entries are not counted and keep their original shape.
		if len(item.Sources) == 0 {
			return errors.New("counted finding item has no source attribution")
		}
		seen := make(map[string]bool, len(item.Sources))
		for _, name := range item.Sources {
			index, ok := indexes[name]
			if !ok || seen[name] {
				return errors.New("finding names an unknown or duplicate source")
			}
			seen[name] = true
			*selectCount(&sources[index])++
		}
	}
	return nil
}

func normalizeSources(manifestDoc manifest, findingsDoc findingsDocument, required map[string]bool) ([]store.ReviewSource, map[string]int, error) {
	roster, actual, degraded, err := mapSources(manifestDoc, findingsDoc)
	if err != nil {
		return nil, nil, err
	}
	if findingsDoc.Sources.Expected != len(manifestDoc.Agents) || findingsDoc.Sources.Expected < 0 {
		return nil, nil, errors.New("inconsistent expected source count")
	}
	sources := make([]store.ReviewSource, 0, len(actual))
	responded := 0
	for name, reported := range actual {
		if !degraded[name] {
			responded++
		}
		sources = append(sources, store.ReviewSource{
			Name: name, Executor: reported.Executor, Required: required[sourceKey(name, reported.Executor)],
			Responded: !degraded[name], Degraded: degraded[name],
		})
	}
	for name, expected := range roster {
		if _, ok := actual[name]; ok || !degraded[name] {
			continue
		}
		// A manifest-only degradation is known non-response evidence, including
		// for an optional source; do not erase it from the normalized card.
		sources = append(sources, store.ReviewSource{
			Name: name, Executor: expected.Executor, Required: required[sourceKey(name, expected.Executor)],
			Responded: false, Degraded: true,
		})
	}
	if findingsDoc.Sources.Reported != responded || findingsDoc.Sources.Reported < 0 {
		return nil, nil, errors.New("inconsistent reported source count")
	}
	slices.SortFunc(sources, func(a, b store.ReviewSource) int {
		if compared := strings.Compare(a.Name, b.Name); compared != 0 {
			return compared
		}
		return strings.Compare(a.Executor, b.Executor)
	})
	indexes := make(map[string]int, len(sources))
	attributionIndexes := make(map[string]int, len(actual))
	for index := range sources {
		indexes[sources[index].Name] = index
		if _, reported := actual[sources[index].Name]; reported {
			attributionIndexes[sources[index].Name] = index
		}
	}
	counts := []struct {
		items       []reviewItem
		selectCount func(*store.ReviewSource) *int
	}{
		{findingsDoc.Findings, func(source *store.ReviewSource) *int { return &source.Findings }},
		{findingsDoc.PreExisting, func(source *store.ReviewSource) *int { return &source.PreExisting }},
		{findingsDoc.OpenQuestions, func(source *store.ReviewSource) *int { return &source.OpenQuestions }},
	}
	for _, count := range counts {
		if err := countItems(count.items, sources, attributionIndexes, count.selectCount); err != nil {
			return nil, nil, err
		}
	}
	return sources, indexes, nil
}

func completenessReasons(required []store.ReviewRequirement, sources []store.ReviewSource, indexes map[string]int) []string {
	reasons := []string{}
	if len(required) == 0 {
		reasons = append(reasons, "required source list is empty")
	}
	for _, requirement := range required {
		index, ok := indexes[requirement.Name]
		if !ok || sourceKey(sources[index].Name, sources[index].Executor) != sourceKey(requirement.Name, requirement.Executor) {
			reasons = append(reasons, "required source missing: "+requirement.Name+" ("+requirement.Executor+")")
			continue
		}
		if sources[index].Degraded {
			reasons = append(reasons, "required source degraded: "+requirement.Name+" ("+requirement.Executor+")")
		}
	}
	slices.Sort(reasons)
	return reasons
}

// Import validates one completed, consistent pair and derives completeness only
// from sources that actually reported in findings.json. Manifest roster entries
// alone never count as responses, and a profile never substitutes an executor.
func Import(binding Binding, reportDir string, required []store.ReviewRequirement) (store.AttachReviewInput, error) {
	var empty store.AttachReviewInput
	if err := binding.Validate(); err != nil {
		return empty, err
	}
	requirements, requiredPairs, err := normalizeRequirements(required)
	if err != nil {
		return empty, err
	}
	manifestDoc, findingsDoc, err := readPair(binding, reportDir)
	if err != nil {
		return empty, err
	}
	sources, indexes, err := normalizeSources(manifestDoc, findingsDoc, requiredPairs)
	if err != nil {
		return empty, err
	}
	reasons := completenessReasons(requirements, sources, indexes)
	return store.AttachReviewInput{
		Repo: binding.Repo, Base: binding.Base, Head: binding.Head,
		RevmuxTask: binding.RevmuxTask, Run: binding.Run, RangeSource: "operator", ReportRef: reportDir,
		Complete: len(reasons) == 0, RequiredSources: requirements, Sources: sources,
		IncompleteReasons: reasons, Findings: len(findingsDoc.Findings),
		PreExisting: len(findingsDoc.PreExisting), OpenQuestions: len(findingsDoc.OpenQuestions),
	}, nil
}
