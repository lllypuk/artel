package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/local"
	"github.com/lllypuk/artel/internal/store"
)

// deliveryEnableRequest is the exact owner PUT body; the pending record keeps
// it verbatim so a resume retries byte-identical content with the original key.
type deliveryEnableRequest struct {
	ExpectedVersion   int64  `json:"expected_version"`
	DeliveryBindingID string `json:"delivery_binding_id"`
	JournalID         string `json:"journal_id"`
	Verifier          string `json:"verifier"`
	RuntimeID         string `json:"runtime_id"`
	ProjectID         string `json:"project_id"`
	PiSessionID       string `json:"pi_session_id"`
	InstanceID        string `json:"instance_id"`
}

// deliveryPending is the durable recovery record for one immutable binding ID.
// It holds the full original request, the retry key and the secret reference;
// JSON output and diagnostics never contain the plaintext delivery token.
type deliveryPending struct {
	Status             string                `json:"status"` // pending, confirmed
	DeliveryBindingID  string                `json:"delivery_binding_id"`
	DatabaseInstanceID string                `json:"database_instance_id"`
	ObserverID         string                `json:"observer_id"`
	RuntimeID          string                `json:"runtime_id"`
	ProjectID          string                `json:"project_id"`
	PiSessionID        string                `json:"pi_session_id"`
	JournalID          string                `json:"journal_id"`
	ObserverSocket     string                `json:"observer_socket"`
	IdempotencyKey     string                `json:"idempotency_key"`
	Verifier           string                `json:"verifier"`
	Output             string                `json:"output"`
	TokenFile          string                `json:"token_file"`
	JournalFile        string                `json:"journal_file"`
	Request            deliveryEnableRequest `json:"request"`
}

// deliveryWriterConfig is the confirmed artifact the explicitly loaded
// extension picks up next to its observer config; it carries no secret.
type deliveryWriterConfig struct {
	Status             string `json:"status"`
	DeliveryBindingID  string `json:"delivery_binding_id"`
	DatabaseInstanceID string `json:"database_instance_id"`
	ObserverSocket     string `json:"observer_socket"`
	TokenFile          string `json:"token_file"`
	JournalFile        string `json:"journal_file"`
	JournalID          string `json:"journal_id"`
}

// deliveryResult is the only CLI output shape: public IDs and file paths, no
// verifier, token, request body or key material.
type deliveryResult struct {
	DeliveryBindingID  string `json:"delivery_binding_id"`
	ObserverID         string `json:"observer_id"`
	Status             string `json:"status"`
	JournalID          string `json:"journal_id"`
	JournalFile        string `json:"journal_file"`
	TokenFile          string `json:"token_file"`
	Output             string `json:"output"`
	DatabaseInstanceID string `json:"database_instance_id"`
}

var errDeliveryUnsafe = errors.New("delivery artifacts unavailable or unsafe")

func validCanonicalID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}

func delivery(args []string, out, errOut io.Writer) int {
	fail := func(code int, message string) int {
		// #nosec G705 -- Fixed local diagnostic; never carries tokens, bodies or request IDs.
		_, _ = fmt.Fprintln(errOut, "artelctl delivery:", message)
		return code
	}
	if len(args) == 0 {
		return fail(2, "expected enable or revoke")
	}
	command := args[0]
	if command != "enable" && command != "revoke" {
		return fail(2, "unknown delivery command")
	}
	flags := flag.NewFlagSet("delivery", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	endpoint := flags.String("endpoint", "http://127.0.0.1:7331", "owner API")
	tokenPath := flags.String("token-file", "", "owner credential")
	_ = flags.Bool("json", false, "JSON output (always enabled)")
	id := flags.String("id", "", "observer UUID")
	binding := flags.String("binding-id", "", "delivery binding UUID")
	runtime := flags.String("runtime-id", "", "runtime UUID")
	project := flags.String("project-id", "", "project UUID")
	piSession := flags.String("pi-session-id", "", "Pi session UUID")
	socket := flags.String("observer-socket", "", "observer Unix socket")
	output := flags.String("output", "", "confirmed writer config path")
	resume := flags.String("resume", "", "pending record path")
	version := flags.Int64("expected-version", -1, "observer version CAS")
	key := flags.String("idempotency-key", "", "UUID retry key")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || local.Endpoint(*endpoint) != nil {
		return fail(2, "invalid arguments")
	}
	paths, err := local.Resolve("", *tokenPath)
	if err != nil {
		return fail(1, "local paths unavailable")
	}
	token, err := local.ReadToken(paths.TokenFile)
	if err != nil {
		return fail(1, "owner credential unavailable or unsafe")
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := ownerClient{*endpoint, token, &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
	if command == "enable" {
		return deliveryEnable(client, deliveryEnableArgs{
			id: *id, runtime: *runtime, project: *project, piSession: *piSession,
			socket: *socket, output: *output, resume: *resume, version: *version, key: *key,
		}, out, fail)
	}
	return deliveryRevoke(client, *id, *binding, *output, *key, out, fail)
}

type deliveryEnableArgs struct {
	id, runtime, project, piSession, socket, output, resume, key string
	version                                                      int64
}

func deliveryResultFor(p deliveryPending, status string) deliveryResult {
	return deliveryResult{
		DeliveryBindingID:  p.DeliveryBindingID,
		ObserverID:         p.ObserverID,
		Status:             status,
		JournalID:          p.JournalID,
		JournalFile:        p.JournalFile,
		TokenFile:          p.TokenFile,
		Output:             p.Output,
		DatabaseInstanceID: p.DatabaseInstanceID,
	}
}

func deliveryEnable(client ownerClient, a deliveryEnableArgs, out io.Writer, fail func(int, string) int) int {
	p, err := deliveryPrepare(client, a)
	if err != nil {
		return fail(1, err.Error())
	}
	// A confirmed bootstrap only reads: it verifies server identity and local
	// artifact integrity, never rewriting journal, token or confirmed config.
	if p.Status == "confirmed" {
		if err = deliveryVerifyConfirmed(client, p); err != nil {
			return fail(1, err.Error())
		}
		return printDelivery(out, p, "confirmed")
	}
	resumeHint := "enable rejected or response lost; resume with --resume " + deliveryPendingPath(p)
	var accepted store.DeliveryBinding
	if err = client.request("PUT", "/v1/observers/"+p.ObserverID+"/delivery", p.IdempotencyKey, p.Request, &accepted); err != nil {
		return fail(1, resumeHint)
	}
	if err = deliveryCheckBinding(p, accepted); err != nil {
		return fail(1, err.Error())
	}
	// A historical enable response never authorizes publication: the current
	// binding identity is re-read and compared before anything is published.
	var confirmed store.DeliveryBinding
	if err = client.request("GET", "/v1/observers/"+p.ObserverID+"/delivery/"+p.DeliveryBindingID, "", nil, &confirmed); err != nil {
		return fail(1, resumeHint)
	}
	if err = deliveryCheckBinding(p, confirmed); err != nil {
		return fail(1, err.Error())
	}
	if err = deliveryEnsureJournal(p); err != nil {
		return fail(1, err.Error())
	}
	writer := deliveryWriterConfig{Status: "confirmed", DeliveryBindingID: p.DeliveryBindingID, DatabaseInstanceID: p.DatabaseInstanceID,
		ObserverSocket: p.ObserverSocket, TokenFile: p.TokenFile, JournalFile: p.JournalFile, JournalID: p.JournalID}
	if err = deliveryPublishConfig(p, writer); err != nil {
		return fail(1, err.Error())
	}
	p.Status = "confirmed"
	if err = writeDeliveryPending(p, true); err != nil {
		return fail(1, "cannot mark bootstrap confirmed")
	}
	return printDelivery(out, p, confirmed.Status)
}

func printDelivery(out io.Writer, p deliveryPending, status string) int {
	b, _ := json.Marshal(deliveryResultFor(p, status))
	return printOutput(out, string(b))
}

// deliveryPrepare returns the durable pending record. A fresh enable pins the
// database identity from the read-only registry, then persists the complete
// request, secret and retry key before the first mutating HTTP request.
func deliveryPrepare(client ownerClient, a deliveryEnableArgs) (deliveryPending, error) {
	var p deliveryPending
	if a.resume != "" {
		if a.id != "" || a.runtime != "" || a.project != "" || a.piSession != "" || a.socket != "" || a.output != "" || a.version >= 0 || a.key != "" {
			return p, errors.New("resume cannot change enable arguments")
		}
		loaded, err := readDeliveryPending(a.resume)
		if err != nil {
			return p, err
		}
		return loaded, deliveryPinDatabase(client, &loaded)
	}
	key, err := validateEnableSelection(a)
	if err != nil {
		return p, err
	}
	var registry store.ObserverRegistry
	if err := client.request("GET", "/v1/observers", "", nil, &registry); err != nil {
		return p, err
	}
	if err := deliverySelectExact(&registry, a); err != nil {
		return p, err
	}
	bindingID := uuid.NewString()
	tokenFile := filepath.Join(filepath.Dir(a.output), "delivery", bindingID, "token")
	if _, err := os.Lstat(tokenFile); !errors.Is(err, os.ErrNotExist) {
		return p, errors.New("delivery artifact already exists or unavailable")
	}
	secret, err := local.InitToken(tokenFile)
	if err != nil {
		return p, errors.New("cannot create private delivery credential")
	}
	journalID := uuid.NewString()
	p = deliveryPending{
		Status: "pending", DeliveryBindingID: bindingID, DatabaseInstanceID: registry.DatabaseInstanceID,
		ObserverID: a.id, RuntimeID: a.runtime, ProjectID: a.project, PiSessionID: a.piSession,
		JournalID: journalID, ObserverSocket: a.socket, IdempotencyKey: key,
		Verifier: local.TokenVerifier(secret), Output: a.output,
		TokenFile:   tokenFile,
		JournalFile: filepath.Join(filepath.Dir(a.output), "delivery", bindingID, "journal.json"),
		Request: deliveryEnableRequest{ExpectedVersion: a.version, DeliveryBindingID: bindingID, JournalID: journalID,
			Verifier: local.TokenVerifier(secret), RuntimeID: a.runtime, ProjectID: a.project, PiSessionID: a.piSession,
			InstanceID: registry.DatabaseInstanceID},
	}
	if err = writeDeliveryPending(p, false); err != nil {
		return p, err
	}
	return p, nil
}

// validateEnableSelection checks the explicit owner-chosen assignment before
// any artifact or request exists; the retry key is generated once and reused.
func validateEnableSelection(a deliveryEnableArgs) (string, error) {
	if !validCanonicalID(a.id) || !validCanonicalID(a.runtime) || !validCanonicalID(a.project) || !validCanonicalID(a.piSession) || a.version < 1 {
		return "", errors.New("id, runtime-id, project-id, pi-session-id and expected-version required")
	}
	if !filepath.IsAbs(a.output) || filepath.Clean(a.output) != a.output {
		return "", errors.New("absolute output path required")
	}
	if !filepath.IsAbs(a.socket) || filepath.Clean(a.socket) != a.socket || len(a.socket) > 107 {
		return "", errors.New("absolute observer socket required")
	}
	if _, err := os.Lstat(a.output); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("output already exists or unavailable; use resume")
	}
	key := a.key
	if key == "" {
		key = uuid.NewString()
	}
	if !validCanonicalID(key) {
		return "", errors.New("invalid idempotency key")
	}
	return key, nil
}

// deliverySelectExact requires the owner-chosen IDs to name the currently
// bound observer, runtime, project and Pi session; nothing is auto-selected.
func deliverySelectExact(registry *store.ObserverRegistry, a deliveryEnableArgs) error {
	if !validCanonicalID(registry.DatabaseInstanceID) {
		return errors.New("invalid database identity")
	}
	for _, observer := range registry.Observers {
		if observer.ObserverID != a.id {
			continue
		}
		if observer.Revoked || observer.Version != a.version || observer.Binding == nil ||
			observer.Binding.RuntimeID != a.runtime || observer.Binding.ProjectID != a.project {
			return errors.New("observer is not bound to the exact selected assignment")
		}
		for _, runtime := range observer.Runtimes {
			if runtime.Registration.RuntimeID == a.runtime {
				if runtime.Registration.PiSessionID != a.piSession || runtime.Disposition != "bound" {
					return errors.New("runtime does not match the exact selected Pi session")
				}
				return nil
			}
		}
		return errors.New("selected runtime is not registered on this observer")
	}
	return errors.New("selected observer not found")
}

// deliveryPinDatabase refuses to retry into a replacement database.
func deliveryPinDatabase(client ownerClient, p *deliveryPending) error {
	var registry store.ObserverRegistry
	if err := client.request("GET", "/v1/observers", "", nil, &registry); err != nil {
		return err
	}
	if registry.DatabaseInstanceID != p.DatabaseInstanceID {
		return errors.New("database identity mismatch")
	}
	return nil
}

// deliveryCheckBinding compares the server-confirmed identity against the
// durable record; a historical or foreign response never authorizes publication.
func deliveryCheckBinding(p deliveryPending, b store.DeliveryBinding) error {
	if b.Status == "revoked" {
		return errors.New("binding is revoked; require a new explicit grant")
	}
	if b.ID != p.DeliveryBindingID || b.ObserverID != p.ObserverID || b.RuntimeID != p.RuntimeID ||
		b.ProjectID != p.ProjectID || b.PiSessionID != p.PiSessionID || b.JournalID != p.JournalID {
		return errors.New("binding identity mismatch; refused to publish")
	}
	return nil
}

// deliveryEnsureJournal creates the journal exactly once with exclusive
// creation; an existing journal is checked, never overwritten.
func deliveryEnsureJournal(p deliveryPending) error {
	doc, _ := json.Marshal(map[string]any{"journal_id": p.JournalID, "entries": map[string]any{}})
	doc = append(doc, '\n')
	if _, err := os.Lstat(p.JournalFile); errors.Is(err, os.ErrNotExist) {
		if err := local.WritePrivateFile(p.JournalFile, doc, false); err != nil {
			return errors.New("cannot create delivery journal; require a new explicit grant")
		}
		return nil
	} else if err != nil {
		return errDeliveryUnsafe
	}
	raw, err := local.ReadPrivateFile(p.JournalFile)
	if err != nil {
		return errDeliveryUnsafe
	}
	var journal struct {
		JournalID string `json:"journal_id"`
	}
	if json.Unmarshal(raw, &journal) != nil || journal.JournalID != p.JournalID {
		return errors.New("journal mismatch; cannot distinguish incomplete creation from loss; require a new explicit grant")
	}
	return nil
}

// deliveryPublishConfig atomically publishes the confirmed writer config only
// after server confirmation and journal creation; an existing identical file
// is left untouched, anything else is never replaced silently.
func deliveryPublishConfig(p deliveryPending, writer deliveryWriterConfig) error {
	data, err := json.Marshal(writer)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err = os.Lstat(p.Output); errors.Is(err, os.ErrNotExist) {
		if err = local.WritePrivateFile(p.Output, data, false); err != nil {
			return errors.New("cannot publish confirmed config; use resume")
		}
		return nil
	} else if err != nil {
		return errDeliveryUnsafe
	}
	raw, err := local.ReadPrivateFile(p.Output)
	if err != nil {
		return errDeliveryUnsafe
	}
	var existing deliveryWriterConfig
	if json.Unmarshal(raw, &existing) != nil || existing != writer {
		return errors.New("confirmed config exists with different content; refused to replace")
	}
	return nil
}

// deliveryVerifyConfirmed reads a confirmed bootstrap without writing: server
// identity, token integrity, journal presence and config identity must match.
func deliveryVerifyConfirmed(client ownerClient, p deliveryPending) error {
	if err := deliveryPinDatabase(client, &p); err != nil {
		return err
	}
	var confirmed store.DeliveryBinding
	if err := client.request("GET", "/v1/observers/"+p.ObserverID+"/delivery/"+p.DeliveryBindingID, "", nil, &confirmed); err != nil {
		return errors.New("binding verification unavailable")
	}
	if err := deliveryCheckBinding(p, confirmed); err != nil {
		return err
	}
	token, err := local.ReadToken(p.TokenFile)
	if err != nil || local.TokenVerifier(token) != p.Verifier {
		return errors.New("delivery credential unavailable or unsafe")
	}
	if err = deliveryEnsureJournal(p); err != nil {
		return err
	}
	raw, err := local.ReadPrivateFile(p.Output)
	if err != nil {
		return errors.New("confirmed config missing; cannot distinguish incomplete publication from loss; require a new explicit grant")
	}
	var config deliveryWriterConfig
	if json.Unmarshal(raw, &config) != nil || config.Status != "confirmed" || config.DeliveryBindingID != p.DeliveryBindingID ||
		config.DatabaseInstanceID != p.DatabaseInstanceID || config.JournalID != p.JournalID ||
		config.ObserverSocket != p.ObserverSocket || config.TokenFile != p.TokenFile || config.JournalFile != p.JournalFile {
		return errors.New("confirmed config mismatch; require a new explicit grant")
	}
	return nil
}

func deliveryPendingPath(p deliveryPending) string {
	return filepath.Join(filepath.Dir(p.Output), "delivery", p.DeliveryBindingID, "pending.json")
}

func readDeliveryPending(path string) (deliveryPending, error) {
	var p deliveryPending
	raw, err := local.ReadPrivateFile(path)
	if err != nil {
		return p, errors.New("pending record unavailable or unsafe")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&p) != nil || dec.Decode(new(any)) != io.EOF {
		return p, errors.New("invalid pending record")
	}
	for _, id := range []string{p.DeliveryBindingID, p.DatabaseInstanceID, p.ObserverID, p.RuntimeID, p.ProjectID, p.PiSessionID, p.JournalID, p.IdempotencyKey} {
		if !validCanonicalID(id) {
			return p, errors.New("invalid pending record")
		}
	}
	if (p.Status != "pending" && p.Status != "confirmed") || !filepath.IsAbs(p.Output) || !filepath.IsAbs(p.ObserverSocket) ||
		len(p.ObserverSocket) > 107 || !filepath.IsAbs(p.TokenFile) || !filepath.IsAbs(p.JournalFile) || p.Verifier == "" {
		return p, errors.New("invalid pending record")
	}
	return p, nil
}

func writeDeliveryPending(p deliveryPending, replace bool) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err = local.WritePrivateFile(deliveryPendingPath(p), append(data, '\n'), replace); err != nil {
		return errors.New("cannot persist pending record")
	}
	return nil
}

// deliveryRevoke revokes the exact binding and clears local authorization
// artifacts: the confirmed config (only when it names this binding), the
// pending record and the delivery token. The journal is retained as history.
func deliveryRevoke(client ownerClient, id, binding, output, key string, out io.Writer, fail func(int, string) int) int {
	if !validCanonicalID(id) || !validCanonicalID(binding) {
		return fail(2, "id and binding-id required")
	}
	if key == "" {
		key = uuid.NewString()
	}
	if !validCanonicalID(key) {
		return fail(2, "invalid idempotency key")
	}
	var revoked store.DeliveryBinding
	if err := client.request("POST", "/v1/observers/"+id+"/delivery/revoke", key, map[string]string{"delivery_binding_id": binding}, &revoked); err != nil {
		return fail(1, err.Error())
	}
	if revoked.ID != binding || revoked.Status != "revoked" {
		return fail(1, "revoke response identity mismatch")
	}
	if output != "" {
		if !filepath.IsAbs(output) || filepath.Clean(output) != output {
			return fail(2, "absolute output path required")
		}
		if err := clearDeliveryArtifacts(output, binding); err != nil {
			return fail(1, err.Error())
		}
	}
	b, _ := json.Marshal(map[string]string{"delivery_binding_id": binding, "observer_id": id, "status": "revoked"})
	return printOutput(out, string(b))
}

func clearDeliveryArtifacts(output, binding string) error {
	if raw, err := local.ReadPrivateFile(output); err == nil {
		var config deliveryWriterConfig
		if json.Unmarshal(raw, &config) != nil || config.DeliveryBindingID != binding {
			return errors.New("local config belongs to another binding; not removed")
		}
		if err := os.Remove(output); err != nil {
			return errors.New("cannot clear confirmed config")
		}
	} else if _, err := os.Lstat(output); err == nil {
		return errors.New("confirmed config unreadable; not removed")
	}
	dir := filepath.Join(filepath.Dir(output), "delivery", binding)
	for _, name := range []string{"pending.json", "token"} {
		path := filepath.Join(dir, name)
		if _, err := os.Lstat(path); err == nil {
			if err := os.Remove(path); err != nil {
				return errors.New("cannot clear local delivery state")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return errors.New("cannot clear local delivery state")
		}
	}
	return nil
}
