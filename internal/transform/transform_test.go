package transform

import "testing"

func transformNamed(t *testing.T, name string) Transform {
	for _, tr := range All {
		if tr.Name == name {
			return tr
		}
	}
	t.Fatalf("no transform %q", name)
	return Transform{}
}

func TestTransformsSucceed(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"UPPERCASE", "héllo", "HÉLLO"},
		{"lowercase", "ÄBC", "äbc"},
		{"Title Case", "hello big world", "Hello Big World"},
		{"Trim Whitespace", "  x \n", "x"},
		{"JSON Pretty-Print", `{"a":1}`, "{\n  \"a\": 1\n}"},
		{"JSON Minify", "{\n \"a\": 1\n}", `{"a":1}`},
		{"URL Encode", "a b&c", "a+b%26c"},
		{"URL Decode", "a+b%26c", "a b&c"},
		{"Base64 Encode", "hi!", "aGkh"},
		{"Base64 Decode", "aGkh", "hi!"},
		{"Base64 Decode", "aGk", "hi"},
		{"Base64 Decode", "aGk_", "hi?"},
	}
	for _, c := range cases {
		got, err := transformNamed(t, c.name).Fn(c.in)
		if err != nil || got != c.want {
			t.Errorf("%s(%q) = %q, %v; want %q", c.name, c.in, got, err, c.want)
		}
	}
}

func TestTransformsFail(t *testing.T) {
	cases := []struct{ name, in string }{
		{"JSON Pretty-Print", `{bad`},
		{"JSON Minify", `[1,`},
		{"URL Decode", "100%"},
		{"URL Decode", "%ff"},
		{"Base64 Decode", "!!!"},
		{"Base64 Decode", "/w=="}, // 0xFF: valid base64, not UTF-8
	}
	for _, c := range cases {
		if got, err := transformNamed(t, c.name).Fn(c.in); err == nil {
			t.Errorf("%s(%q) = %q, want error", c.name, c.in, got)
		}
	}
}
