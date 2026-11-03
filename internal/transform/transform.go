package transform

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// Transform rewrites an entry's text.
type Transform struct {
	Name string
	Fn   func(string) (string, error)
}

// All lists the transforms in menu order.
var All = []Transform{
	{"UPPERCASE", func(s string) (string, error) { return strings.ToUpper(s), nil }},
	{"lowercase", func(s string) (string, error) { return strings.ToLower(s), nil }},
	{"Title Case", func(s string) (string, error) { return cases.Title(language.Und).String(s), nil }},
	{"Trim Whitespace", func(s string) (string, error) { return strings.TrimSpace(s), nil }},
	{"JSON Pretty-Print", func(s string) (string, error) {
		var b bytes.Buffer
		if err := json.Indent(&b, []byte(s), "", "  "); err != nil {
			return "", err
		}
		return b.String(), nil
	}},
	{"JSON Minify", func(s string) (string, error) {
		var b bytes.Buffer
		if err := json.Compact(&b, []byte(s)); err != nil {
			return "", err
		}
		return b.String(), nil
	}},
	{"URL Encode", func(s string) (string, error) { return url.QueryEscape(s), nil }},
	{"URL Decode", func(s string) (string, error) {
		out, err := url.QueryUnescape(s)
		if err != nil {
			return "", err
		}
		if !utf8.Valid([]byte(out)) {
			return "", errors.New("decoded data is not text")
		}
		return out, nil
	}},
	{"Base64 Encode", func(s string) (string, error) { return base64.StdEncoding.EncodeToString([]byte(s)), nil }},
	{"Base64 Decode", func(s string) (string, error) {
		in := strings.TrimSpace(s)
		var b []byte
		var firstErr error
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			var err error
			if b, err = enc.DecodeString(in); err == nil {
				firstErr = nil
				break
			} else if firstErr == nil {
				firstErr = err
			}
		}
		if firstErr != nil {
			return "", firstErr
		}
		if !utf8.Valid(b) {
			return "", errors.New("decoded data is not text")
		}
		return string(b), nil
	}},
}
