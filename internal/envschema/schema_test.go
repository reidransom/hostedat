package envschema

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestEquivalentFormatsAndCanonicalRoundTrip(t *testing.T) {
	jsonSource := `{"VITE_TOKEN":{"type":"string"},"PORT":{"required":false,"type":"number"},"MODE":{"type":"enum","values":["production"," preview "]}}`
	yamlSource := "VITE_TOKEN:\n  type: string\nPORT:\n  type: number\n  required: false\nMODE:\n  type: enum\n  values: [production, ' preview ']\n"
	j, err := Parse(strings.NewReader(jsonSource), JSON)
	if err != nil {
		t.Fatal(err)
	}
	y, err := Parse(strings.NewReader(yamlSource), YAML)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(j, y) {
		t.Fatal("formats produced different rules")
	}
	want := `{"MODE":{"type":"enum","required":true,"values":["production"," preview "]},"PORT":{"type":"number","required":false},"VITE_TOKEN":{"type":"string","required":true}}`
	for _, schema := range []Schema{j, y} {
		canonical, err := schema.CanonicalJSON()
		if err != nil || string(canonical) != want {
			t.Fatalf("unexpected canonical declaration: %s, %v", canonical, err)
		}
		roundTrip, err := Parse(strings.NewReader(string(canonical)), JSON)
		if err != nil || !reflect.DeepEqual(roundTrip, schema) {
			t.Fatal("canonical JSON did not round-trip")
		}
		if err := schema.Validate(map[string]string{"VITE_TOKEN": "secret", "MODE": "production"}); err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(map[string]string{"MODE": "production"}); err == nil {
			t.Fatal("VITE_ declaration did not require a secret")
		}
	}
	for _, source := range []string{"{}", "---\n{}\n...\n"} {
		s, err := Parse(strings.NewReader(source), YAML)
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.CanonicalJSON()
		if err != nil || string(b) != "{}" || s == nil {
			t.Fatal("explicit empty declaration not preserved")
		}
	}
}

func TestRejectInvalidDeclarations(t *testing.T) {
	cases := []struct {
		name   string
		format Format
		source string
	}{
		{"json trailing", JSON, `{} {}`},
		{"json yaml syntax", JSON, `A: {type: string}`},
		{"json trailing comma", JSON, `{"A":{"type":"string",}}`},
		{"json duplicate name", JSON, `{"A":{"type":"string"},"A":{"type":"number"}}`},
		{"json duplicate field", JSON, `{"A":{"type":"string","type":"number"}}`},
		{"json unknown", JSON, `{"A":{"type":"string","default":"MARKER"}}`},
		{"json coercion", JSON, `{"A":{"type":"string","required":"true"}}`},
		{"json type coercion", JSON, `{"A":{"type":1}}`},
		{"json null", JSON, `null`},
		{"json null required", JSON, `{"A":{"type":"string","required":null}}`},
		{"json null values", JSON, `{"A":{"type":"enum","values":null}}`},
		{"json invalid utf8", JSON, "{\"A\":{\"type\":\"str\xffing\"}}"},
		{"yaml duplicate name", YAML, "A: {type: string}\nA: {type: number}"},
		{"yaml duplicate field", YAML, "A: {type: string, type: number}"},
		{"yaml unknown", YAML, "A: {type: string, default: MARKER}"},
		{"yaml nonstring name", YAML, "123: {type: string}"},
		{"yaml bool name", YAML, "true: {type: string}"},
		{"yaml nonstring field", YAML, "A: {true: string}"},
		{"yaml nonstring type", YAML, "A: {type: 123}"},
		{"yaml coercion", YAML, "A: {type: string, required: 'true'}"},
		{"yaml bool casing", YAML, "A: {type: string, required: True}"},
		{"yaml bool legacy", YAML, "A: {type: string, required: yes}"},
		{"yaml enum coercion", YAML, "A: {type: enum, values: [true]}"},
		{"yaml multi document", YAML, "A: {type: string}\n---\n{}"},
		{"yaml empty second", YAML, "{}\n---\n"},
		{"yaml syntax", YAML, "A: [MARKER"},
		{"yaml alias", YAML, "A: &rule {type: string}\nB: *rule"},
		{"yaml merge", YAML, "A: {<<: {type: string}}"},
		{"yaml custom tag", YAML, "A: !rule {type: string}"},
		{"empty", YAML, ""},
		{"array", YAML, "[]"},
		{"grouped", YAML, "server: {A: {type: string}}"},
		{"versioned", YAML, "version: 1\nA: {type: string}"},
		{"missing type", YAML, "A: {required: true}"},
		{"unsupported type", YAML, "A: {type: MARKER}"},
		{"enum missing values", YAML, "A: {type: enum}"},
		{"enum empty values", YAML, "A: {type: enum, values: []}"},
		{"enum duplicate", YAML, "A: {type: enum, values: [MARKER, MARKER]}"},
		{"nonenum values", YAML, "A: {type: string, values: []}"},
		{"invalid name", YAML, "'MARKER-invalid': {type: string}"},
		{"unsupported format", Format("toml"), "MARKER"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tc.source), tc.format)
			if err == nil {
				t.Fatal("invalid declaration accepted")
			}
			if strings.Contains(err.Error(), "MARKER") {
				t.Fatal("diagnostic disclosed input")
			}
			var diagnostic *Diagnostic
			if !errors.As(err, &diagnostic) {
				t.Fatal("error was not a safe diagnostic")
			}
		})
	}
}

func TestNameGrammar(t *testing.T) {
	for _, name := range []string{"A", "_", "a9_", "VITE_TOKEN", "ASSETS"} {
		if err := ValidateName(name); err != nil {
			t.Fatalf("valid static name rejected: %v", err)
		}
	}
	for _, name := range []string{"", "1A", "a-b", "a.b", " A", "A\n", "É", "A/B"} {
		if err := ValidateName(name); err == nil {
			t.Fatal("invalid name accepted")
		}
	}
}

func TestValueConstraintsAndPresence(t *testing.T) {
	cases := []struct {
		kind           Type
		values         []string
		valid, invalid []string
	}{
		{String, nil, []string{"hello", " ", "\n multiline \n", ""}, []string{"\xff"}},
		{Number, nil, []string{"0", "-12.5", "1e3", "1E-3"}, []string{"", "NaN", "Infinity", "1e9999", "0x10", "01", "+1", ".1", " 1", "1\n", `"1"`, "null", "true"}},
		{Boolean, nil, []string{"true", "false"}, []string{"", "TRUE", "False", "1", "0", "yes", " true", "false\n"}},
		{URL, nil, []string{"https://example.test/path?q=a#b", "mailto:user@example.test", "file:///tmp/value"}, []string{"", "/relative", "example.test", "https://bad host", " https://example.test", "https://example.test\n", "https://%zz"}},
		{Email, nil, []string{"user@example.test", "user+tag@example.test"}, []string{"", "not-an-address", "User <user@example.test>", " user@example.test", "user@example.test\n"}},
		{JSONValue, nil, []string{`{"a":[1,true,null]}`, `"text"`, "null", "  [1] \n", "42"}, []string{"", "{", "NaN", "{} {}"}},
		{Enum, []string{"yes", " no ", ""}, []string{"yes", " no ", ""}, []string{"no", "YES", " yes"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			s := Schema{"VALUE": {Type: tc.kind, Required: false, Values: tc.values}}
			if err := s.Validate(nil); err != nil {
				t.Fatal("optional absence rejected")
			}
			for _, value := range tc.valid {
				if err := s.Validate(map[string]string{"VALUE": value}); err != nil {
					t.Fatalf("valid %s rejected: %v", tc.kind, err)
				}
			}
			for _, value := range tc.invalid {
				if err := s.Validate(map[string]string{"VALUE": value}); err == nil {
					t.Fatalf("invalid %s accepted", tc.kind)
				}
			}
			rule := s["VALUE"]
			rule.Required = true
			s["VALUE"] = rule
			if err := s.Validate(nil); err == nil {
				t.Fatal("required absence accepted")
			}
			if err := s.Validate(map[string]string{"VALUE": ""}); err == nil {
				t.Fatal("required empty accepted")
			}
		})
	}
}

func TestSingleValueAndValueFreeDiagnostics(t *testing.T) {
	s := Schema{"A": {Type: Number, Required: true}, "B": {Type: String, Required: true}}
	if err := s.ValidateValue("A", "12", true); err != nil {
		t.Fatal("merge check required unrelated name")
	}
	if err := s.ValidateValue("OTHER", "MARKER", true); err == nil || strings.Contains(err.Error(), "MARKER") {
		t.Fatal("undeclared merge check was unsafe")
	}
	for _, kind := range []Type{Number, Boolean, URL, Email, JSONValue, Enum} {
		rule := Rule{Type: kind, Values: nil}
		if kind == Enum {
			rule.Values = []string{"allowed"}
		}
		s := Schema{"A": rule}
		err := s.Validate(map[string]string{"A": "DISPOSABLE_SECRET_MARKER\n"})
		if err == nil || strings.Contains(err.Error(), "DISPOSABLE_SECRET_MARKER") {
			t.Fatal("value validation was unsafe")
		}
	}
	invalid := Schema{"A": {Type: Type("MARKER")}}
	if _, err := invalid.CanonicalJSON(); err == nil || strings.Contains(err.Error(), "MARKER") {
		t.Fatal("constructed schema was not checked safely")
	}
	if err := invalid.Validate(nil); err == nil {
		t.Fatal("invalid constructed schema accepted")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("DISPOSABLE_SECRET_MARKER") }

type countingReader struct {
	remaining int
	read      int
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.remaining)
	for i := range n {
		p[i] = ' '
	}
	r.remaining -= n
	r.read += n
	return n, nil
}

func TestBoundedInputAndSafeReadErrors(t *testing.T) {
	for _, format := range []Format{JSON, YAML} {
		r := &countingReader{remaining: MaxDocumentBytes * 2}
		if _, err := Parse(r, format); err == nil {
			t.Fatal("oversized document accepted")
		}
		if r.read != MaxDocumentBytes+1 {
			t.Fatalf("read was not bounded: %d", r.read)
		}
		source := "{}" + strings.Repeat(" ", MaxDocumentBytes-2)
		if _, err := Parse(strings.NewReader(source), format); err != nil {
			t.Fatalf("exact bound rejected: %v", err)
		}
		_, err := Parse(failingReader{}, format)
		if err == nil || strings.Contains(err.Error(), "DISPOSABLE_SECRET_MARKER") {
			t.Fatal("read error leaked")
		}
	}
	if _, err := (Schema{"A": {Type: Enum, Values: []string{strings.Repeat("x", MaxDocumentBytes)}}}).CanonicalJSON(); err == nil {
		t.Fatal("oversized canonical document accepted")
	}
}
