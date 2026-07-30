package capabilities

import (
	"slices"
	"testing"
)

func TestToolCatalogIsVersionedAndSorted(t *testing.T) {
	catalog := ToolCatalog()
	if len(catalog) == 0 {
		t.Fatal("ToolCatalog() is empty")
	}
	ids := CommonToolIDs()
	if !slices.IsSorted(ids) {
		t.Fatalf("CommonToolIDs() = %#v, want sorted", ids)
	}
	if !slices.Contains(ids, "pytest") || !slices.Contains(ids, "uv") {
		t.Fatalf("CommonToolIDs() = %#v, want Python project tools", ids)
	}
	for _, descriptor := range catalog {
		if descriptor.SchemaVersion != CatalogSchemaVersion {
			t.Fatalf("descriptor %#v has wrong schema version", descriptor)
		}
		if descriptor.ID == "" || descriptor.Label == "" || descriptor.Category == "" {
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
