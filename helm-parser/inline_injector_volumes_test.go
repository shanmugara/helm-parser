package helm_parser

import (
	"strings"
	"testing"
)

func TestInjectInlineContainerSpec_IgnoreVolumes(t *testing.T) {
	input := `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: test-container
          image: nginx:latest
          env:
          - name: TEST_VAR
            value: test
      volumes:
        - name: my-volume
          configMap:
            name: my-config`

	result, err := injectInlineContainerSpec(input, "inject-blocks.yaml")
	if err != nil {
		t.Fatalf("injectInlineContainerSpec failed: %v", err)
	}

	// Should have envFrom injected in the container
	if !strings.Contains(result, "kubernetes-services-endpoint") {
		t.Error("Expected 'kubernetes-services-endpoint' to be injected in container")
	}

	// Count occurrences of "- name:" at the right indent levels
	// We expect: container name, volume name, and env var name (3 total)
	nameCount := strings.Count(result, "- name:")
	if nameCount < 2 {
		t.Errorf("Expected at least 2 '- name:' occurrences (container + volume), got %d", nameCount)
	}

	// Verify volumes section is unchanged - should still have "- name: my-volume"
	if !strings.Contains(result, "- name: my-volume") {
		t.Error("Volume definition should be preserved")
	}

	// Verify envFrom is NOT injected in volumes section
	lines := strings.Split(result, "\n")
	inVolumesSection := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "volumes:") {
			inVolumesSection = true
		}

		// If we're in volumes section and find envFrom, that's an error
		if inVolumesSection && strings.HasPrefix(trimmed, "envFrom:") {
			t.Errorf("envFrom should NOT be injected in volumes section (line %d)", i+1)
		}

		// Exit volumes section when indent decreases to spec level or lower
		if inVolumesSection && (trimmed == "containers:" || strings.HasPrefix(trimmed, "initContainers:")) {
			inVolumesSection = false
		}
	}

	t.Log("✓ Volumes section test passed - injection only in containers")

	t.Log("\nModified content:")
	for i, line := range lines {
		t.Logf("%3d: %s", i+1, line)
	}
}

func TestInjectInlineContainerSpec_MultipleContainersWithVolumes(t *testing.T) {
	input := `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: app-container
          image: app:latest
        - name: sidecar-container
          image: sidecar:latest
      volumes:
        - name: shared-data
          emptyDir: {}
        - name: config-volume
          configMap:
            name: app-config`

	result, err := injectInlineContainerSpec(input, "inject-blocks.yaml")
	if err != nil {
		t.Fatalf("injectInlineContainerSpec failed: %v", err)
	}

	// Should have envFrom injected in both containers
	envFromCount := strings.Count(result, "envFrom:")
	if envFromCount != 2 {
		t.Errorf("Expected 2 'envFrom:' blocks (one per container), got %d", envFromCount)
	}

	// Should have 4 "- name:" (2 containers + 2 volumes)
	nameCount := strings.Count(result, "- name:")
	if nameCount != 4 {
		t.Errorf("Expected 4 '- name:' occurrences (2 containers + 2 volumes), got %d", nameCount)
	}

	// Verify volumes are unchanged
	if !strings.Contains(result, "- name: shared-data") {
		t.Error("First volume should be preserved")
	}
	if !strings.Contains(result, "- name: config-volume") {
		t.Error("Second volume should be preserved")
	}

	t.Log("✓ Multiple containers with volumes test passed")
}
