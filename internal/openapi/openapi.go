// Package openapi publishes the OpenAPI document that specifies the HTTP API.
//
// The document is authoritative for the API: the Go server in internal/api and
// the TypeScript types in web/ts/api/types.ts are both generated from it. It
// lives at the repository root, because a generator's input is not a detail of
// any one package, and is embedded by main — the only package that can embed a
// file there — and handed to serve, which publishes it at /openapi.json and
// /openapi.yaml.
package openapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/goccy/go-yaml"
)

// Document is the OpenAPI document as written, with the JSON form derived from
// it. Keeping one source and converting is what stops the two representations
// from drifting.
type Document struct {
	yamlDocument []byte
	jsonDocument func() ([]byte, error)
}

// New wraps the document. The JSON form is converted once, on first use.
func New(yamlDocument []byte) *Document {
	return &Document{
		yamlDocument: yamlDocument,
		jsonDocument: sync.OnceValues(func() ([]byte, error) {
			var value any
			if err := yaml.Unmarshal(yamlDocument, &value); err != nil {
				return nil, fmt.Errorf("parse openapi.yaml: %w", err)
			}
			encoded, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("encode openapi.json: %w", err)
			}
			return append(encoded, '\n'), nil
		}),
	}
}

// YAML is the document as written.
func (d *Document) YAML() []byte { return d.yamlDocument }

// JSON is the same document as JSON.
func (d *Document) JSON() ([]byte, error) { return d.jsonDocument() }

// YAMLHandler serves /openapi.yaml.
func (d *Document) YAMLHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		if r.Method != http.MethodHead {
			_, _ = w.Write(d.yamlDocument)
		}
	})
}

// JSONHandler serves /openapi.json.
func (d *Document) JSONHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		document, err := d.JSON()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodHead {
			_, _ = w.Write(document)
		}
	})
}

// CatalogHandler publishes the API's discovery links using the RFC 9727
// Linkset profile. Origin-relative references keep discovery independent of
// the hostname used to reach this server.
func CatalogHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", `application/linkset+json; profile="https://www.rfc-editor.org/info/rfc9727"`)
		w.Header().Set("Link", `</.well-known/api-catalog>; rel="api-catalog"`)
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write([]byte(`{"linkset":[{"anchor":"/api/","service-desc":[{"href":"/openapi.yaml","type":"application/yaml"}]}]}` + "\n"))
	})
}
