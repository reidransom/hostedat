package envschema

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDotenvLiteralValues(t *testing.T) {
	t.Setenv("DOTENV_SHELL_MARKER", "must-not-expand")
	source := "# comment\r\n  export URL = \"https://example.test/#fragment\" # ignored\r\n" +
		"SINGLE='  hash# and $URL ${DOTENV_SHELL_MARKER}  '\r\n" +
		"DOUBLE=\"  $URL ${DOTENV_SHELL_MARKER} $(false) `false`  \"\r\n" +
		"PLAIN=  $URL ${DOTENV_SHELL_MARKER} $(false) `false`  \t\r\n" +
		"MULTI=\"line one\r\nNAME=not-an-assignment # literal\r\nline three\" # comment\r\n" +
		"SINGLE_MULTI='first\r\nsecond'\r\nESCAPES=\"a\\nb\\rc\\t\\\"d\\\\e\"\r\n" +
		"TRIM=  internal  space \t # comment # second\r\n" +
		"HASH=abc#literal\r\nEMPTY=\r\nCOMMENT= # empty\r\nQUOTED_EMPTY=''\r\n" +
		"SPACE=\" \t \"\r\nNOT_EXPORT=export x\r\nFINAL=last"
	got, err := ParseDotenv(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"URL":          "https://example.test/#fragment",
		"SINGLE":       "  hash# and $URL ${DOTENV_SHELL_MARKER}  ",
		"DOUBLE":       "  $URL ${DOTENV_SHELL_MARKER} $(false) `false`  ",
		"PLAIN":        "$URL ${DOTENV_SHELL_MARKER} $(false) `false`",
		"MULTI":        "line one\nNAME=not-an-assignment # literal\nline three",
		"SINGLE_MULTI": "first\nsecond", "ESCAPES": "a\nb\rct\"d\\e",
		"TRIM": "internal  space", "HASH": "abc#literal", "EMPTY": "", "COMMENT": "",
		"QUOTED_EMPTY": "", "SPACE": " \t ", "NOT_EXPORT": "export x", "FINAL": "last",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dotenv semantics mismatch: got %#v, want %#v", got, want)
	}
	if os.Getenv("DOTENV_SHELL_MARKER") != "must-not-expand" {
		t.Fatal("dotenv parser changed process environment")
	}
}

func TestDotenvRejectsDuplicatesAndSafeSyntaxErrors(t *testing.T) {
	for _, source := range []string{
		"A=DOTENV_SECRET_MARKER\nA=replacement",
		"A=first\n export\tA = DOTENV_SECRET_MARKER",
		"A='first\nsecond'\nA=DOTENV_SECRET_MARKER",
		"A=\nA=DOTENV_SECRET_MARKER",
		"DOTENV_SECRET_MARKER", "A: DOTENV_SECRET_MARKER", "=DOTENV_SECRET_MARKER",
		"1BAD=DOTENV_SECRET_MARKER", "BAD-NAME=DOTENV_SECRET_MARKER", "B AD=DOTENV_SECRET_MARKER",
		"A=\"DOTENV_SECRET_MARKER", "A='DOTENV_SECRET_MARKER", "A='ok' DOTENV_SECRET_MARKER",
		"A=\"ok\" B=DOTENV_SECRET_MARKER", "A=DOTENV_SECRET_MARKER\x00", "A=\xffDOTENV_SECRET_MARKER",
		"A=DOTENV_SECRET_MARKER\rB=next",
	} {
		got, err := ParseDotenv(strings.NewReader(source))
		if err == nil || got != nil {
			t.Fatal("invalid/duplicate dotenv input was accepted")
		}
		if strings.Contains(err.Error(), "DOTENV_SECRET_MARKER") {
			t.Fatal("dotenv error disclosed input")
		}
	}
	if got, err := ParseDotenv(strings.NewReader("# only comments\n\n")); err != nil || len(got) != 0 || got == nil {
		t.Fatal("empty dotenv document did not produce an explicit empty map")
	}
}

type dotenvFailReader struct{}

func (dotenvFailReader) Read([]byte) (int, error) { return 0, errors.New("DOTENV_SECRET_MARKER") }

func TestDotenvBoundedReads(t *testing.T) {
	for _, size := range []int{MaxDotenvBytes, MaxDotenvBytes + 1} {
		source := strings.NewReader("A=" + strings.Repeat("x", size-2))
		values, err := ParseDotenv(source)
		if size == MaxDotenvBytes && (err != nil || len(values["A"]) != size-2) {
			t.Fatal("exact input limit or long line rejected")
		}
		if size > MaxDotenvBytes && (err == nil || values != nil) {
			t.Fatal("oversized input accepted")
		}
	}
	source := &io.LimitedReader{R: strings.NewReader(strings.Repeat("x", MaxDotenvBytes*2)), N: MaxDotenvBytes * 2}
	if _, err := ParseDotenv(source); err == nil || source.N != MaxDotenvBytes-1 {
		t.Fatal("dotenv read was not bounded to limit plus one")
	}
	if _, err := ParseDotenv(dotenvFailReader{}); err == nil || strings.Contains(err.Error(), "DOTENV_SECRET_MARKER") {
		t.Fatal("reader error was not safely replaced")
	}
}

func TestDotenvDoesNotExecuteSubstitutions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "must-not-be-created")
	value := "$(touch " + path + ") `touch " + path + "` ${HOME} $HOME"
	for _, source := range []string{"A=" + value, "A=\"" + value + "\"", "A='" + value + "'"} {
		got, err := ParseDotenv(strings.NewReader(source))
		if err != nil || got["A"] != value {
			t.Fatal("substitution was not preserved literally")
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dotenv input executed a command")
	}
}
