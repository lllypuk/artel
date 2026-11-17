// artelctl provides local Artel diagnostics.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/local"
)

func run(args []string, out, errOut io.Writer) int {
	if len(args) == 1 {
		switch args[0] {
		case "--help", "-h":
			return printOutput(out, "Usage: artelctl --help | --version | status|snapshot --json [--endpoint URL] [--token-file PATH]")
		case "--version":
			return printOutput(out, "artelctl 0.1.0-dev")
		}
	}
	if len(args) > 0 && args[0] == "observer" {
		return observer(args[1:], out, errOut)
	}
	if len(args) > 0 && (args[0] == "status" || args[0] == "snapshot") {
		return diagnostic(args, out, errOut)
	}
	_, _ = fmt.Fprintln(errOut, "artelctl: expected --help, --version, status or snapshot")
	return 2
}

func diagnostic(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("artelctl", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	endpoint := flags.String("endpoint", "http://127.0.0.1:7331", "local endpoint")
	tokenPath := flags.String("token-file", "", "credential file")
	asJSON := flags.Bool("json", false, "JSON output")
	if err := flags.Parse(args[1:]); err != nil || !*asJSON || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(errOut, "artelctl: expected --json and valid options")
		return 2
	}
	if err := local.Endpoint(*endpoint); err != nil {
		_, _ = fmt.Fprintln(errOut, "artelctl: invalid local endpoint")
		return 2
	}
	path := "/healthz"
	token := ""
	if args[0] == "snapshot" {
		path = "/v1/snapshot"
		paths, err := local.Resolve("", *tokenPath)
		if err != nil {
			_, _ = fmt.Fprintln(errOut, "artelctl: local paths unavailable")
			return 1
		}
		var errToken error
		token, errToken = local.ReadToken(paths.TokenFile)
		if errToken != nil {
			_, _ = fmt.Fprintln(errOut, "artelctl: credential unavailable or unsafe")
			return 1
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *endpoint+path, nil)
	if err != nil {
		return 1
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	// #nosec G704 -- Endpoint is validated as numeric IPv4 loopback; proxies and redirects are disabled.
	response, err := client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "artelctl: local API unavailable")
		return 1
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintln(errOut, "artelctl: API request failed")
		return 1
	}
	const limit = 32 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(body) > limit || !validResponse(args[0], body) {
		_, _ = fmt.Fprintln(errOut, "artelctl: invalid API response")
		return 1
	}
	return printOutput(out, string(body))
}

func validResponse(command string, body []byte) bool {
	if command == "status" {
		var status struct {
			Status string `json:"status"`
		}
		return json.Unmarshal(body, &status) == nil && status.Status == "ok"
	}
	var snapshot struct {
		InstanceID string            `json:"instance_id"`
		EventSeq   *int64            `json:"event_seq"`
		Projects   []json.RawMessage `json:"projects"`
		Tasks      []json.RawMessage `json:"tasks"`
		Queues     []json.RawMessage `json:"queues"`
	}
	if json.Unmarshal(body, &snapshot) != nil {
		return false
	}
	id, err := uuid.Parse(snapshot.InstanceID)
	return err == nil && id.String() == snapshot.InstanceID && snapshot.EventSeq != nil && *snapshot.EventSeq >= 0 && snapshot.Projects != nil && snapshot.Tasks != nil && snapshot.Queues != nil
}

func printOutput(out io.Writer, text string) int {
	if _, err := fmt.Fprintln(out, text); err != nil {
		return 1
	}
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
