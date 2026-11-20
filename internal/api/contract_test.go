package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
)

var contractOnce sync.Once
var contractDoc *openapi3.T
var contractErr error

func contract(t *testing.T) *openapi3.T {
	t.Helper()
	contractOnce.Do(func() {
		loader := openapi3.NewLoader()
		loader.IsExternalRefsAllowed = true
		contractDoc, contractErr = loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
		if contractErr == nil {
			contractErr = contractDoc.Validate(context.Background())
		}
	})
	if contractErr != nil {
		t.Fatal(contractErr)
	}
	return contractDoc
}

func TestOpenAPIValid(t *testing.T) {
	_, h := fixture(t)
	request(t, h, "GET", "/healthz", "", "", 200)
	doc := contract(t)
	count := 0
	for _, p := range doc.Paths.Map() {
		count += len(p.Operations())
	}
	if count != 27 {
		t.Fatalf("operations: %d", count)
	}
}

// Every request through the API test helper validates its response against the
// actual operation, including errors. Successful requests validate input too.
func checkContract(t *testing.T, r *http.Request, w *httptest.ResponseRecorder) {
	t.Helper()
	r = r.Clone(r.Context())
	r.URL.Scheme = "http"
	r.URL.Host = "127.0.0.1:7331"
	r.Host = r.URL.Host
	router, err := legacy.NewRouter(contract(t))
	if err != nil {
		t.Fatal(err)
	}
	route, params, err := router.FindRoute(r)
	if err != nil {
		t.Fatal(err)
	}
	input := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
	if w.Code < 300 {
		if err = openapi3filter.ValidateRequest(t.Context(), input); err != nil {
			t.Fatalf("request contract: %v", err)
		}
	}
	output := &openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: w.Code, Header: w.Header(), Options: &openapi3filter.Options{IncludeResponseStatus: true}}
	output.SetBodyBytes(bytes.Clone(w.Body.Bytes()))
	if err = openapi3filter.ValidateResponse(t.Context(), output); err != nil {
		t.Fatalf("response contract: %v\n%s", err, w.Body)
	}
}
