package build

import (
	"fmt"
	"strings"
	"testing"
)

func TestReplaceArgs(t *testing.T) {
	dump := []byte(`{"Replace":[
		{"Old":{"Path":"golang.org/x/sys"},"New":{"Path":"github.com/golang/sys","Version":"v0.36.0"}},
		{"Old":{"Path":"golang.org/x/text","Version":"v0.3.0"},"New":{"Path":"github.com/golang/text","Version":"v0.3.8"}},
		{"Old":{"Path":"example.com/local"},"New":{"Path":"../local"}},
		{"Old":{"Path":"github.com/dewald-L/dock"},"New":{"Path":"../dock"}}
	]}`)
	replaced := map[string]bool{DockModule: true}
	got := replaceArgs(dump, "/src/plugin", replaced)
	want := "[-replace=golang.org/x/sys=github.com/golang/sys@v0.36.0" +
		" -replace=golang.org/x/text@v0.3.0=github.com/golang/text@v0.3.8" +
		" -replace=example.com/local=/src/local]"
	if fmt.Sprint(got) != want {
		t.Fatalf("got  %v\nwant %s", got, want)
	}
	// a second module replacing the same paths doesn't override the first
	if again := replaceArgs(dump, "/other", replaced); len(again) != 0 {
		t.Fatalf("already replaced paths must be skipped, got %v", again)
	}
	if replaceArgs([]byte("not json"), "/x", replaced) != nil {
		t.Fatal("bad JSON gives no arguments")
	}
}

func TestGoEnvFor(t *testing.T) {
	cases := []struct {
		proxy, private string
		want           []string
		not            []string
	}{
		// Go's default: fall back to direct on any proxy error
		{DefaultGoProxy, "", []string{"GOPROXY=https://proxy.golang.org|direct", "GOPRIVATE=github.com/dewald-L/*"}, nil},
		{"", "", []string{"GOPROXY=https://proxy.golang.org|direct"}, nil},
		// a proxy the user chose is kept
		{"https://goproxy.corp.example", "", nil, []string{"GOPROXY="}},
		{"off", "", nil, []string{"GOPROXY="}},
		// GOPRIVATE is extended, not replaced, and not duplicated
		{DefaultGoProxy, "example.com/*", []string{"GOPRIVATE=example.com/*,github.com/dewald-L/*"}, nil},
		{DefaultGoProxy, "github.com/dewald-L/*", nil, []string{"GOPRIVATE="}},
		{DefaultGoProxy, "a.com/*, github.com/dewald-L/*", nil, []string{"GOPRIVATE="}},
	}
	for _, c := range cases {
		got := strings.Join(goEnvFor(c.proxy, c.private), "\n")
		if !strings.Contains(got, "GOFLAGS=-mod=mod") || !strings.Contains(got, "GOWORK=off") {
			t.Errorf("%q %q: base vars missing:\n%s", c.proxy, c.private, got)
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%q %q: want %s in:\n%s", c.proxy, c.private, w, got)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%q %q: must not set %s:\n%s", c.proxy, c.private, n, got)
			}
		}
	}
}
