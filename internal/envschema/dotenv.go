package envschema

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/joho/godotenv"
)

// MaxDotenvBytes is the shared limit for a complete dotenv input, not each line.
const MaxDotenvBytes = 1 << 20

func inputError(constraint string) error {
	return errors.New("environment input: " + constraint)
}

// ParseDotenv reads a bounded UTF-8 dotenv document without consulting or changing
// the process environment. Assignments use NAME=value with optional export. It
// rejects duplicates before handing an assignment to the maintained parser.
// Dollar substitutions and backticks are always literal, never interpolated.
func ParseDotenv(r io.Reader) (map[string]string, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxDotenvBytes+1))
	if err != nil {
		return nil, inputError("cannot read dotenv document")
	}
	if len(data) > MaxDotenvBytes {
		return nil, inputError("dotenv document exceeds size limit")
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, inputError("dotenv document must be UTF-8 without NUL")
	}
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if bytes.IndexByte(data, '\r') >= 0 {
		return nil, inputError("dotenv line endings must be LF or CRLF")
	}
	values := make(map[string]string)
	for len(data) > 0 {
		line, rest, _ := bytes.Cut(data, []byte("\n"))
		lineBytes := len(line)
		line = bytes.TrimLeft(line, " \t")
		if len(line) == 0 || line[0] == '#' {
			data = rest
			continue
		}
		if bytes.HasPrefix(line, []byte("export ")) || bytes.HasPrefix(line, []byte("export\t")) {
			line = bytes.TrimLeft(line[len("export"):], " \t")
		}
		key, value, found := bytes.Cut(line, []byte("="))
		name := string(bytes.TrimRight(key, " \t"))
		if !found {
			return nil, inputError("expected dotenv NAME=value assignment")
		}
		if err := ValidateName(name); err != nil {
			return nil, err
		}
		if _, exists := values[name]; exists {
			return nil, diagnostic(name, "duplicate dotenv assignment")
		}
		value = bytes.TrimLeft(value, " \t")
		if len(value) > 0 && (value[0] == '\'' || value[0] == '"') {
			// Find the complete quoted record, including physical newlines. The
			// closing-quote rule deliberately matches godotenv's pinned version:
			// a quote immediately following a backslash is escaped.
			quoted := data[lineBytes-len(value):]
			end := 1
			for end < len(quoted) && (quoted[end] != quoted[0] || quoted[end-1] == '\\') {
				end++
			}
			if end == len(quoted) {
				return nil, inputError("unterminated dotenv quoted value")
			}
			value = quoted[:end+1]
			tail, next, _ := bytes.Cut(quoted[end+1:], []byte("\n"))
			tail = bytes.TrimLeft(tail, " \t")
			if len(tail) > 0 && tail[0] != '#' {
				return nil, inputError("unexpected text after dotenv quoted value")
			}
			rest = next
		} else {
			// A comment begins at # only at value start or after space/tab.
			// Strip the first comment before godotenv's last-comment handling.
			for i, c := range value {
				if c == '#' && (i == 0 || value[i-1] == ' ' || value[i-1] == '\t') {
					value = value[:i]
					break
				}
			}
		}
		// godotenv has no interpolation-off option. NUL is forbidden above,
		// so shielding every dollar with NUL is reversible and ensures its
		// interpolation code never sees a substitution (including $()).
		literal := strings.ReplaceAll(string(value), "$", "\x00")
		parsed, err := godotenv.Unmarshal(name + "=" + literal)
		if err != nil || len(parsed) != 1 {
			return nil, inputError("invalid dotenv assignment")
		}
		values[name] = strings.ReplaceAll(parsed[name], "\x00", "$")
		data = rest
	}
	return values, nil
}
