package agentauth

import "testing"

func TestSetJSONCEditsOnlyTheChangedValue(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		path           []string
	}{
		{
			name: "replace",
			in:   "{\n  // keep\n  \"a\": {\"b\": \"old\" /* x */},\n}\n",
			path: []string{"a", "b"},
			want: "{\n  // keep\n  \"a\": {\"b\": \"new\" /* x */},\n}\n",
		},
		{
			name: "insert into an existing object",
			in:   "{\"a\": {\"c\": 1} // note\n}",
			path: []string{"a", "b"},
			want: "{\"a\": {\"c\": 1, \"b\":\"new\"} // note\n}",
		},
		{
			name: "create missing parents",
			in:   "{\"x\": 1}",
			path: []string{"a", "b"},
			want: "{\"x\": 1, \"a\":{\"b\":\"new\"}}",
		},
		{
			name: "empty object",
			in:   "{}",
			path: []string{"a"},
			want: "{\"a\":\"new\"}",
		},
		{
			name: "keeps a BOM",
			in:   "\ufeff{\"a\": \"old\"}",
			path: []string{"a"},
			want: "\ufeff{\"a\": \"new\"}",
		},
		{
			name: "string that looks like a comment",
			in:   "{\"u\": \"http://x//y\", \"a\": \"old\"}",
			path: []string{"a"},
			want: "{\"u\": \"http://x//y\", \"a\": \"new\"}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := setJSONC([]byte(tc.in), tc.path, []byte(`"new"`))
			if err != nil || string(got) != tc.want {
				t.Fatalf("setJSONC = %q, %v; want %q", got, err, tc.want)
			}
			var parsed map[string]any
			if err := jsoncUnmarshal(got, &parsed); err != nil {
				t.Fatalf("result does not parse: %v", err)
			}
		})
	}
	if _, err := setJSONC([]byte(`{"a": [1]}`), []string{"a", "b"}, []byte(`"new"`)); err == nil {
		t.Fatal("descended into a non-object")
	}
}
