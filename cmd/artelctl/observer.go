package main

import (
	"bytes"
	"context"
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

type ownerClient struct {
	endpoint, token string
	client          *http.Client
}

func (c ownerClient) request(method, path, key string, body, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	// #nosec G704 -- Numeric loopback endpoint validated before constructing client; no proxy or redirects.
	response, err := c.client.Do(req)
	if err != nil {
		return errors.New("local API unavailable; provisioning can be resumed")
	}
	defer func() { _ = response.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(response.Body, 64*1024*1024+1))
	if err != nil || len(b) > 64*1024*1024 {
		return errors.New("invalid API response")
	}
	if response.StatusCode != 200 {
		return errors.New("API operation rejected")
	}
	if json.Unmarshal(b, result) != nil {
		return errors.New("invalid API response")
	}
	return nil
}
func observer(args []string, out, errOut io.Writer) int {
	fail := func(code int, message string) int {
		// #nosec G705 -- Plain terminal diagnostic; messages are fixed local errors, never API input.
		_, _ = fmt.Fprintln(errOut, "artelctl observer:", message)
		return code
	}
	if len(args) == 0 {
		return fail(2, "expected provision, list, bind, unbind, revoke or prune")
	}
	command := args[0]
	switch command {
	case "provision", "list", "bind", "unbind", "revoke", "prune":
	default:
		return fail(2, "unknown observer command")
	}
	flags := flag.NewFlagSet("observer", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	endpoint := flags.String("endpoint", "http://127.0.0.1:7331", "owner API")
	tokenPath := flags.String("token-file", "", "owner credential")
	_ = flags.Bool("json", false, "JSON output (always enabled)")
	name := flags.String("name", "", "observer label")
	socket := flags.String("observer-socket", "", "observer socket")
	output := flags.String("output", "", "private config path")
	resume := flags.String("resume", "", "resume provisioning config")
	id := flags.String("id", "", "observer UUID")
	var runtimes []string
	flags.Func("runtime-id", "runtime UUID (repeat for prune)", func(id string) error {
		runtimes = append(runtimes, id)
		return nil
	})
	project := flags.String("project-id", "", "project UUID")
	version := flags.Int64("expected-version", -1, "observer version CAS")
	key := flags.String("idempotency-key", "", "UUID retry key")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || local.Endpoint(*endpoint) != nil {
		return fail(2, "invalid arguments")
	}
	if command == "prune" && !store.ValidObserverPruneIDs(runtimes) {
		return fail(2, "prune requires 1–8 unique canonical non-nil --runtime-id UUIDs")
	}
	if command != "prune" && len(runtimes) > 1 {
		return fail(2, "--runtime-id may only be repeated for prune")
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
	if command == "provision" {
		result, err := provisionObserver(client, *name, *socket, *output, *resume)
		if err != nil {
			return fail(1, err.Error())
		}
		b, _ := json.Marshal(result)
		return printOutput(out, string(b))
	}
	if command == "list" {
		var registry store.ObserverRegistry
		if err = client.request("GET", "/v1/observers", "", nil, &registry); err != nil {
			return fail(1, err.Error())
		}
		b, _ := json.Marshal(registry)
		return printOutput(out, string(b))
	}
	return observerMutation(client, command, id, project, runtimes, version, key, out, fail)
}

func observerMutation(client ownerClient, command string, id, project *string, runtimes []string, version *int64, key *string, out io.Writer, fail func(int, string) int) int {
	if u, e := uuid.Parse(*id); e != nil || u.String() != *id || *version < 0 {
		return fail(2, "id and expected-version required")
	}
	if *key == "" {
		*key = uuid.NewString()
	}
	if u, e := uuid.Parse(*key); e != nil || u.String() != *key {
		return fail(2, "invalid idempotency key")
	}
	method, suffix := "POST", "/"+command
	body := map[string]any{"expected_version": *version}
	if command == "prune" {
		body["expected_runtime_ids"] = runtimes
	}
	if command == "bind" || command == "unbind" {
		method, suffix = "PATCH", "/binding"
		body["runtime_id"], body["project_id"] = nil, nil
		if command == "bind" {
			if len(runtimes) != 1 {
				return fail(2, "runtime-id and project-id required")
			}
			for _, s := range []string{runtimes[0], *project} {
				if u, e := uuid.Parse(s); e != nil || u.String() != s {
					return fail(2, "runtime-id and project-id required")
				}
			}
			body["runtime_id"], body["project_id"] = runtimes[0], *project
		}
	}
	var result store.ObserverOwnerResult
	if err := client.request(method, "/v1/observers/"+*id+suffix, *key, body, &result); err != nil {
		return fail(1, err.Error())
	}
	b, _ := json.Marshal(result)
	return printOutput(out, string(b))
}

func prepareObserver(client ownerClient, name, socket, output, resume string) (local.ObserverConfig, error) {
	var result local.ObserverConfig
	var c local.ObserverConfig
	var err error
	if resume != "" {
		if name != "" || socket != "" || output != "" {
			return result, errors.New("resume cannot change provisioning arguments")
		}
		output = resume
		c, err = local.ReadObserverConfig(output, false)
		if err != nil {
			return result, errors.New("pending config unavailable or unsafe")
		}
	} else {
		if name == "" || !filepath.IsAbs(output) || !filepath.IsAbs(socket) || len(socket) > 107 {
			return result, errors.New("name, absolute output and observer-socket required")
		}
		if _, e := os.Lstat(output); !errors.Is(e, os.ErrNotExist) {
			return result, errors.New("config already exists or unavailable; use resume")
		}
		var registry store.ObserverRegistry
		if err = client.request("GET", "/v1/observers", "", nil, &registry); err != nil {
			return result, err
		}
		if u, e := uuid.Parse(registry.DatabaseInstanceID); e != nil || u.String() != registry.DatabaseInstanceID {
			return result, errors.New("invalid database identity")
		}
		// A fresh sidecar name cannot reuse any existing credential.
		tokenFile := output + ".token"
		if _, e := os.Lstat(tokenFile); !errors.Is(e, os.ErrNotExist) {
			return result, errors.New("token artifact already exists or unavailable")
		}
		token, e := local.InitToken(tokenFile)
		if e != nil {
			return result, errors.New("cannot create private observer credential")
		}
		c = local.ObserverConfig{Status: "pending", ObserverID: uuid.NewString(), DatabaseInstanceID: registry.DatabaseInstanceID, ObserverSocket: socket, TokenFile: tokenFile, ProvisioningKey: uuid.NewString(), Payload: local.ObserverProvision{Label: name, Verifier: local.TokenVerifier(token)}}
		if err = local.WriteObserverConfig(output, c, false); err != nil {
			return result, errors.New("cannot persist pending config")
		}
	}
	return c, nil
}

func provisionObserver(client ownerClient, name, socket, output, resume string) (store.ObserverOwnerResult, error) {
	var result store.ObserverOwnerResult
	c, err := prepareObserver(client, name, socket, output, resume)
	if err != nil {
		return result, err
	}
	if resume != "" {
		output = resume
	}
	// Always pin database identity before a retry; never provision into replacement DB.
	var registry store.ObserverRegistry
	if err = client.request("GET", "/v1/observers", "", nil, &registry); err != nil {
		return result, err
	}
	if registry.DatabaseInstanceID != c.DatabaseInstanceID {
		return result, errors.New("database identity mismatch")
	}
	if err = client.request("PUT", "/v1/observers/"+c.ObserverID, c.ProvisioningKey, c.Payload, &result); err != nil {
		return result, err
	}
	if result.ObserverID != c.ObserverID || result.DatabaseInstanceID != c.DatabaseInstanceID || result.Version < 1 {
		return result, errors.New("provisioning response identity mismatch")
	}
	c.Status = "confirmed"
	if err = local.WriteObserverConfig(output, c, true); err != nil {
		return result, errors.New("cannot confirm config; use resume")
	}
	return result, nil
}
