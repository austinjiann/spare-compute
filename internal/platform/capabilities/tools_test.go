package capabilities

import (
	"slices"
	"testing"
)

func TestToolCatalogIsCompleteAndSorted(t *testing.T) {
	if len(toolCatalog) == 0 {
		t.Fatal("tool catalog is empty")
	}
	ids := CommonToolIDs()
	if !slices.IsSorted(ids) {
		t.Fatalf("CommonToolIDs() = %#v, want sorted", ids)
	}
	if !slices.Contains(ids, "pytest") || !slices.Contains(ids, "uv") {
		t.Fatalf("CommonToolIDs() = %#v, want Python project tools", ids)
	}
	for _, descriptor := range toolCatalog {
		if descriptor.ID == "" || descriptor.Label == "" {
			t.Fatalf("descriptor %#v is incomplete", descriptor)
		}
	}
}

func TestToolLabels(t *testing.T) {
	if got := ToolLabel("go"); got != "Go" {
		t.Fatalf("ToolLabel(go) = %q", got)
	}
	if got := ToolLabel("xcodebuild"); got != "Xcode" {
		t.Fatalf("ToolLabel(xcodebuild) = %q", got)
	}
	if got := ToolLabel("custom-tool"); got != "custom-tool" {
		t.Fatalf("ToolLabel(custom-tool) = %q", got)
	}
	if got := ToolListLabel([]string{"docker", "go"}); got != "Docker and Go" {
		t.Fatalf("ToolListLabel(two) = %q", got)
	}
	if got := ToolListLabel([]string{"docker", "go", "swift"}); got != "Docker, Go, and Swift" {
		t.Fatalf("ToolListLabel(three) = %q", got)
	}
}
