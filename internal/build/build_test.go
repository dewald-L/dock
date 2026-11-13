package build

import (
	"fmt"
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
