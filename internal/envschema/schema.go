// Package envschema parses secret declarations and validates string bindings.
// Its local input helpers read explicitly scoped files without writes, process
// environment mutation, shell execution, network access, or database access.
package envschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/mail"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// MaxDocumentBytes bounds source declarations and canonical metadata to 1 MiB.
const MaxDocumentBytes = 1 << 20

// Format selects a source syntax, independently of filesystem discovery.
type Format string

const (
	JSON Format = "json"
	YAML Format = "yaml"
)

// Type is a constraint on a binding's string value; no conversion is performed.
type Type string

const (
	String    Type = "string"
	Number    Type = "number"
	Boolean   Type = "boolean"
	URL       Type = "url"
	Email     Type = "email"
	JSONValue Type = "json"
	Enum      Type = "enum"
)

// Rule declares a secret. Required is explicit in the normalized model; parsing
// defaults it to true. Values is present only for Enum and retains exact strings.
type Rule struct {
	Type     Type     `json:"type"`
	Required bool     `json:"required"`
	Values   []string `json:"values,omitempty"`
}

// Schema is a flat declaration. All names, including VITE_ names, denote secrets.
// An empty Schema is an explicit empty declaration, not an absent manifest.
type Schema map[string]Rule

// Diagnostic contains only a validated variable name and a fixed constraint.
// Parser errors and supplied values are never wrapped or retained.
type Diagnostic struct {
	Name       string
	Constraint string
}

func (d *Diagnostic) Error() string {
	if d.Name == "" {
		return "environment declaration: " + d.Constraint
	}
	return "environment variable " + d.Name + ": " + d.Constraint
}

func diagnostic(name, constraint string) error {
	return &Diagnostic{Name: name, Constraint: constraint}
}

var namePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateName checks only the portable, case-sensitive ASCII name grammar.
// Runtime-reserved names and site-specific collisions require a separate check
// by the caller inside its database coordination protocol.
func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return diagnostic("", "invalid name (expected [A-Za-z_][A-Za-z0-9_]*)")
	}
	return nil
}

// Parse reads at most MaxDocumentBytes+1 bytes and accepts exactly one mapping.
// JSON uses strict JSON syntax; both formats reject duplicates and coercion.
func Parse(r io.Reader, format Format) (Schema, error) {
	if format != JSON && format != YAML {
		return nil, diagnostic("", "unsupported format")
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxDocumentBytes+1))
	if err != nil {
		return nil, diagnostic("", "cannot read document")
	}
	if len(data) > MaxDocumentBytes {
		return nil, diagnostic("", "document exceeds size limit")
	}
	if !utf8.Valid(data) {
		return nil, diagnostic("", "document must be UTF-8")
	}
	if format == JSON && !json.Valid(data) {
		return nil, diagnostic("", "invalid JSON document")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nil, diagnostic("", "invalid declaration document")
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, diagnostic("", "expected one document")
	}
	if len(doc.Content) != 1 || !mapping(doc.Content[0]) {
		return nil, diagnostic("", "expected flat mapping")
	}
	root := doc.Content[0]
	schema := make(Schema, len(root.Content)/2)
	for i := 0; i < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		if !scalar(key, "!!str") {
			return nil, diagnostic("", "names must be string keys")
		}
		name := key.Value
		if err := ValidateName(name); err != nil {
			return nil, err
		}
		if _, exists := schema[name]; exists {
			return nil, diagnostic(name, "duplicate declaration")
		}
		rule, err := parseRule(name, value)
		if err != nil {
			return nil, err
		}
		schema[name] = rule
	}
	if err := schema.Check(); err != nil {
		return nil, err
	}
	return schema, nil
}

func scalar(n *yaml.Node, tag string) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == tag && n.Anchor == ""
}
func mapping(n *yaml.Node) bool {
	return n.Kind == yaml.MappingNode && n.Tag == "!!map" && n.Anchor == ""
}

func parseRule(name string, n *yaml.Node) (Rule, error) {
	rule := Rule{Required: true}
	if !mapping(n) {
		return rule, diagnostic(name, "expected rule mapping")
	}
	seen := make(map[string]bool, 3)
	for i := 0; i < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		if !scalar(key, "!!str") {
			return rule, diagnostic(name, "rule fields must be string keys")
		}
		if seen[key.Value] {
			return rule, diagnostic(name, "duplicate rule field")
		}
		seen[key.Value] = true
		switch key.Value {
		case "type":
			if !scalar(value, "!!str") {
				return rule, diagnostic(name, "type must be a string")
			}
			rule.Type = Type(value.Value)
		case "required":
			if !scalar(value, "!!bool") || (value.Value != "true" && value.Value != "false") {
				return rule, diagnostic(name, "required must be true or false")
			}
			rule.Required = value.Value == "true"
		case "values":
			if value.Kind != yaml.SequenceNode || value.Tag != "!!seq" || value.Anchor != "" {
				return rule, diagnostic(name, "values must be a string list")
			}
			rule.Values = make([]string, 0, len(value.Content))
			for _, item := range value.Content {
				if !scalar(item, "!!str") {
					return rule, diagnostic(name, "values must be a string list")
				}
				rule.Values = append(rule.Values, item.Value)
			}
		default:
			return rule, diagnostic(name, "unknown rule field")
		}
	}
	return rule, nil
}

// Names returns declaration names in stable lexical order.
func (s Schema) Names() []string {
	names := make([]string, 0, len(s))
	for name := range s {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Check also validates schemas constructed programmatically by API consumers.
func (s Schema) Check() error {
	for _, name := range s.Names() {
		if err := ValidateName(name); err != nil {
			return err
		}
		rule := s[name]
		switch rule.Type {
		case String, Number, Boolean, URL, Email, JSONValue:
			if rule.Values != nil {
				return diagnostic(name, "values is only allowed for enum")
			}
		case Enum:
			if len(rule.Values) == 0 {
				return diagnostic(name, "enum requires nonempty values")
			}
			seen := make(map[string]bool, len(rule.Values))
			for _, value := range rule.Values {
				if !utf8.ValidString(value) {
					return diagnostic(name, "enum values must be UTF-8")
				}
				if seen[value] {
					return diagnostic(name, "duplicate enum value")
				}
				seen[value] = true
			}
		default:
			return diagnostic(name, "unsupported type")
		}
	}
	return nil
}

// CanonicalJSON returns compact, sorted-key JSON with explicit required flags.
// It never serializes binding values. Nil schemas serialize as an explicit {}.
func (s Schema) CanonicalJSON() ([]byte, error) {
	if err := s.Check(); err != nil {
		return nil, err
	}
	if s == nil {
		return []byte("{}"), nil
	}
	data, err := json.Marshal(s)
	if err != nil {
		return nil, diagnostic("", "cannot serialize declaration")
	}
	if len(data) > MaxDocumentBytes {
		return nil, diagnostic("", "document exceeds size limit")
	}
	return data, nil
}

// Validate checks all declared names; undeclared bindings are ignored. Map lookup
// preserves absence versus an explicitly empty value. Optional means absence is
// permitted, not that invalid supplied values are ignored.
func (s Schema) Validate(values map[string]string) error {
	if err := s.Check(); err != nil {
		return err
	}
	for _, name := range s.Names() {
		value, present := values[name]
		if err := s.validateValue(name, value, present); err != nil {
			return err
		}
	}
	return nil
}

// ValidateValue checks one supplied declaration without requiring other names.
// Undeclared names fail, making this suitable for schema-backed merge inputs.
func (s Schema) ValidateValue(name, value string, present bool) error {
	if err := s.Check(); err != nil {
		return err
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	if _, ok := s[name]; !ok {
		return diagnostic(name, "not declared")
	}
	return s.validateValue(name, value, present)
}

func (s Schema) validateValue(name, value string, present bool) error {
	rule := s[name]
	if !present {
		if rule.Required {
			return diagnostic(name, "required value is missing")
		}
		return nil
	}
	if rule.Required && value == "" {
		return diagnostic(name, "required value is empty")
	}
	if !utf8.ValidString(value) {
		return diagnostic(name, "value must be UTF-8")
	}
	valid := true
	switch rule.Type {
	case String:
	case Number:
		// JSON numeric grammar excludes whitespace, hex, NaN and infinities.
		var number json.Number
		valid = json.Unmarshal([]byte(value), &number) == nil && number.String() == value && value != ""
		if valid {
			n, err := strconv.ParseFloat(value, 64)
			valid = err == nil && !math.IsInf(n, 0) && !math.IsNaN(n)
		}
	case Boolean:
		valid = value == "true" || value == "false"
	case URL:
		u, err := url.Parse(value)
		valid = err == nil && u.IsAbs() && !strings.ContainsAny(value, " \t\r\n") && (u.Host != "" || u.Opaque != "" || u.Path != "")
	case Email:
		a, err := mail.ParseAddress(value)
		valid = err == nil && a.Address == value && !strings.ContainsAny(value, " \t\r\n")
	case JSONValue:
		valid = json.Valid([]byte(value))
	case Enum:
		valid = false
		for _, choice := range rule.Values {
			if value == choice {
				valid = true
				break
			}
		}
	}
	if !valid {
		return diagnostic(name, fmt.Sprintf("must satisfy %s constraint", rule.Type))
	}
	return nil
}
