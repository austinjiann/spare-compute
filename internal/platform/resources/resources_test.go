package resources

import "testing"

func TestStaticReportsBoundedLogicalCPUCount(t *testing.T) {
	snapshot := Static()
	if snapshot.LogicalCPUCount == 0 {
		t.Fatal("logical CPU count = 0")
	}
	if snapshot.LogicalCPUCount > 4096 {
		t.Fatalf("logical CPU count = %d, want <= 4096", snapshot.LogicalCPUCount)
	}
}
