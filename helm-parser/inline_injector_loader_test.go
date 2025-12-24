package helm_parser

import (
	"strings"
	"testing"
)

func TestLoadInjectorBlocks(t *testing.T) {
	blocks, err := loadInjectorBlocks("inject-blocks.yaml")
	if err != nil {
		t.Fatalf("loadInjectorBlocks failed: %v", err)
	}

	// Check that we have the expected categories
	expectedCategories := []string{"allPods", "allContainers", "criticalDsPods", "controlPlanePods"}
	for _, category := range expectedCategories {
		if _, exists := blocks[category]; !exists {
			t.Errorf("Expected category '%s' not found in blocks", category)
		}
	}

	// Check allPods has blocks
	if len(blocks["allPods"]) == 0 {
		t.Error("Expected allPods to have at least one block")
	}

	// Check allContainers has the envFrom block
	if len(blocks["allContainers"]) == 0 {
		t.Error("Expected allContainers to have at least one block")
	}

	// Verify allContainers contains envFrom configMapRef
	found := false
	for _, block := range blocks["allContainers"] {
		if strings.Contains(block, "envFrom:") && strings.Contains(block, "kubernetes-services-endpoint") {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected allContainers to contain envFrom block with kubernetes-services-endpoint")
	}

	// Check controlPlanePods has blocks
	if len(blocks["controlPlanePods"]) == 0 {
		t.Error("Expected controlPlanePods to have at least one block")
	}

	// Verify controlPlanePods contains affinity
	found = false
	for _, block := range blocks["controlPlanePods"] {
		if strings.Contains(block, "affinity:") && strings.Contains(block, "node-role.kubernetes.io/control-plane") {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected controlPlanePods to contain affinity block")
	}

	t.Log("✓ loadInjectorBlocks test passed")
	t.Logf("\nLoaded %d categories:", len(blocks))
	for category, blockList := range blocks {
		t.Logf("  %s: %d blocks", category, len(blockList))
	}
}

func TestLoadInjectorBlocks_Structure(t *testing.T) {
	blocks, err := loadInjectorBlocks("inject-blocks.yaml")
	if err != nil {
		t.Fatalf("loadInjectorBlocks failed: %v", err)
	}

	// Print out the structure for inspection
	t.Log("\nInjector Blocks Structure:")
	for category, blockList := range blocks {
		t.Logf("\nCategory: %s", category)
		for i, block := range blockList {
			t.Logf("  Block %d:\n%s", i+1, block)
		}
	}
}
