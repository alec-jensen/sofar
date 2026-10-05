package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

var (
	specOnce   sync.Once
	specRouter routers.Router
	specErr    error
)

func loadSpec() (routers.Router, error) {
	specOnce.Do(func() {
		doc, err := openapi3.NewLoader().LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
		if err != nil {
			specErr = err
			return
		}
		if specErr = doc.Validate(context.Background()); specErr != nil {
			return
		}
		specRouter, specErr = gorillamux.NewRouter(doc)
	})
	return specRouter, specErr
}

func TestAPI_OpenAPISpecIsValid(t *testing.T) {
	if _, err := loadSpec(); err != nil {
		t.Fatal(err)
	}
}

// checkContract fails the test if a response does not match api/openapi.yaml.
// Paths that are not in the spec (static files) are skipped.
func checkContract(t *testing.T, req *http.Request, status int, header http.Header, body []byte) {
	t.Helper()
	router, err := loadSpec()
	if err != nil {
		t.Fatal(err)
	}
	probe := httptest.NewRequest(req.Method, "http://localhost"+req.URL.Path, nil)
	route, params, err := router.FindRoute(probe)
	if err != nil {
		t.Errorf("%s %s is not in api/openapi.yaml", req.Method, req.URL.Path)
		return
	}
	in := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{Request: probe, PathParams: params, Route: route},
		Status:                 status,
		Header:                 header,
	}
	in.SetBodyBytes(body)
	if err = openapi3filter.ValidateResponse(context.Background(), in); err != nil {
		t.Errorf("%s %s -> %d does not match the spec: %v\nbody: %s", req.Method, req.URL.Path, status, err, body)
	}
}
