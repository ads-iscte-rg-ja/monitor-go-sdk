package monitorsdk_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	monitorsdk "github.com/ads-iscte-rg-ja/monitor-go-sdk"
)

// The SDK writes into the remote's buffer: its levels must be the ones the
// schema's CHECK accepts (ADR-0015).
func TestLevelsMatchTheBufferSchema(t *testing.T) {
	schema, err := os.ReadFile(remoteSchema)
	if err != nil {
		t.Fatalf("read remote schema (monitor-remote must be cloned next to this repo): %v", err)
	}
	m := regexp.MustCompile(`level TEXT NOT NULL CHECK\(level IN \(([^)]*)\)\)`).FindSubmatch(schema)
	if m == nil {
		t.Fatal("level CHECK not found in the remote schema")
	}
	var checked []string
	for _, value := range strings.Split(string(m[1]), ",") {
		checked = append(checked, strings.Trim(strings.TrimSpace(value), "'"))
	}
	got, want := slices.Sorted(slices.Values(monitorsdk.Levels)), slices.Sorted(slices.Values(checked))
	if !slices.Equal(got, want) {
		t.Fatalf("SDK levels %v, buffer schema %v", got, want)
	}
}
