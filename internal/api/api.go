// Package api embeds the OpenAPI document that specifies the HTTP API.
//
// The document is authoritative for the API. It lives in the repository, is
// embedded in the binary, and is served at /openapi.json and /openapi.yaml.
package api

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/goccy/go-yaml"
)

//go:embed openapi.yaml
var yamlDocument []byte

// YAML is the document as written.
func YAML() []byte { return yamlDocument }

// JSON is the same document converted once, on first use. Keeping one source
// and converting is what stops the two representations from drifting.
var JSON = sync.OnceValues(func() ([]byte, error) {
	var value any
	if err := yaml.Unmarshal(yamlDocument, &value); err != nil {
		return nil, fmt.Errorf("parse openapi.yaml: %w", err)
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode openapi.json: %w", err)
	}
	return append(encoded, '\n'), nil
})

// YAMLHandler serves /openapi.yaml.
func YAMLHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(yamlDocument)
	})
}

// JSONHandler serves /openapi.json.
func JSONHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		document, err := JSON()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(document)
	})
}
