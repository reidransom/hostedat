// Package environment owns persisted worker values and their in-memory decryption.
package environment

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"syscall"
)

var ErrStorage = errors.New("environment storage is invalid or unavailable")
var ErrInput = errors.New("invalid environment input")
var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

const FormatVersion = 1

type Keys struct {
	active string
	keys   map[string]cipher.AEAD
}

// LoadKeys reads only an operator-provisioned, owner-only regular file. Keys are
// base64-encoded 32-byte random values; key IDs must never be reused for new keys.
func LoadKeys(path string) (*Keys, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("cannot open environment key file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || (info.Mode().Perm() != 0600 && info.Mode().Perm() != 0400) {
		return nil, errors.New("environment key file must be a restricted regular file (0600 or 0400)")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("environment key file must be owned by the server operator")
	}
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(data) > 65536 {
		return nil, errors.New("invalid environment key file")
	}
	var doc struct {
		Active string            `json:"active"`
		Keys   map[string]string `json:"keys"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	// Reject duplicate object keys before decoding can overwrite them.
	if !uniqueJSON(data) || dec.Decode(&doc) != nil || len(doc.Keys) == 0 || len(doc.Keys) > 32 {
		return nil, errors.New("invalid environment key file")
	}
	k := &Keys{active: doc.Active, keys: make(map[string]cipher.AEAD, len(doc.Keys))}
	for id, encoded := range doc.Keys {
		if !keyIDPattern.MatchString(id) {
			return nil, errors.New("invalid environment key file")
		}
		raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(raw) != 32 {
			return nil, errors.New("invalid environment key file")
		}
		block, err := aes.NewCipher(raw)
		if err != nil {
			return nil, ErrStorage
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, ErrStorage
		}
		k.keys[id] = aead
	}
	if k.keys[k.active] == nil {
		return nil, errors.New("active environment key is missing")
	}
	return k, nil
}

func uniqueJSON(data []byte) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	var value func() bool
	value = func() bool {
		tok, err := d.Token()
		if err != nil {
			return false
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				t, e := d.Token()
				key, ok := t.(string)
				if e != nil || !ok || seen[key] {
					return false
				}
				seen[key] = true
				if !value() {
					return false
				}
			}
		case '[':
			for d.More() {
				if !value() {
					return false
				}
			}
		default:
			return false
		}
		_, err = d.Token()
		return err == nil
	}
	if !value() {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}

func associated(site, name string, version int) []byte {
	b, _ := json.Marshal(struct {
		Site    string
		Name    string
		Version int
	}{site, name, version})
	return b
}

func (k *Keys) encrypt(site, name, value string) (string, []byte, []byte, error) {
	if k == nil || k.keys[k.active] == nil {
		return "", nil, nil, ErrStorage
	}
	a := k.keys[k.active]
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", nil, nil, ErrStorage
	}
	return k.active, nonce, a.Seal(nil, nonce, []byte(value), associated(site, name, FormatVersion)), nil
}
func (k *Keys) decrypt(site, name string, version int, keyID string, nonce, ciphertext []byte) (string, error) {
	if k == nil || version != FormatVersion {
		return "", ErrStorage
	}
	a := k.keys[keyID]
	if a == nil || len(nonce) != a.NonceSize() {
		return "", ErrStorage
	}
	plain, err := a.Open(nil, nonce, ciphertext, associated(site, name, version))
	if err != nil {
		return "", ErrStorage
	}
	return string(plain), nil
}
