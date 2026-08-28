package openapi

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml"
)

// Operation is what the document says one operation accepts.
//
// It is read out of the document rather than restated in Go, because both
// rules below are rules about what the document declares: a table of names
// beside the generated code would be a second copy of it, free to drift.
type Operation struct {
	// QueryParameters are the query parameters the operation declares. Any
	// other one is refused rather than ignored, exactly as an unrecognised body
	// field is: a parameter the server never reads is a thing the client
	// believes it said.
	QueryParameters map[string]bool
	// TakesBody says whether the operation declares a request body. One that
	// does not refuses a body rather than ignoring it, for the same reason.
	TakesBody bool
}

// Operations reads the operations out of the document, keyed by operation ID —
// the identifier the generated server reports for the request it is serving.
func (d *Document) Operations() (map[string]Operation, error) {
	var doc struct {
		Paths      map[string]map[string]any `yaml:"paths"`
		Components struct {
			Parameters map[string]any `yaml:"parameters"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(d.yamlDocument, &doc); err != nil {
		return nil, fmt.Errorf("parse openapi.yaml: %w", err)
	}

	operations := map[string]Operation{}
	for path, item := range doc.Paths {
		// A parameters list on the path applies to every operation under it.
		shared, err := queryNames(item["parameters"], doc.Components.Parameters)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		for method, raw := range item {
			if method == "parameters" {
				continue
			}
			operation, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s %s: not an operation", method, path)
			}
			id, ok := operation["operationId"].(string)
			if !ok {
				return nil, fmt.Errorf("%s %s: no operationId", method, path)
			}

			names, err := queryNames(operation["parameters"], doc.Components.Parameters)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", id, err)
			}
			for name := range shared {
				names[name] = true
			}
			operations[id] = Operation{
				QueryParameters: names,
				TakesBody:       operation["requestBody"] != nil,
			}
		}
	}
	return operations, nil
}

// queryNames reads the names of the query parameters in one parameters list,
// resolving a $ref against the document's own parameter components. Nothing
// else is resolvable: a reference out of the document would name something the
// binary does not carry.
func queryNames(raw any, components map[string]any) (map[string]bool, error) {
	names := map[string]bool{}
	if raw == nil {
		return names, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("parameters is not a list")
	}

	for _, entry := range list {
		param, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("parameter is not an object")
		}
		if ref, ok := param["$ref"].(string); ok {
			name, found := strings.CutPrefix(ref, "#/components/parameters/")
			if !found {
				return nil, fmt.Errorf("cannot resolve %s", ref)
			}
			if param, ok = components[name].(map[string]any); !ok {
				return nil, fmt.Errorf("cannot resolve %s", ref)
			}
		}
		if param["in"] != "query" {
			continue
		}
		name, ok := param["name"].(string)
		if !ok {
			return nil, fmt.Errorf("query parameter without a name")
		}
		names[name] = true
	}
	return names, nil
}
