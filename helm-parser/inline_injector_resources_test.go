package helm_parser

import (
	"strings"
	"testing"
)

func TestInjectInlineContainerSpec_ExistingResources(t *testing.T) {
	input := `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: test-container
          image: nginx:latest
          resources:
            limits:
              cpu: "1"
              memory: "2Gi"
          env:
          - name: TEST_VAR
            value: test`

	result, err := injectInlineContainerSpec(input, "inject-blocks.yaml", "")
	if err != nil {
		t.Fatalf("injectInlineContainerSpec failed: %v", err)
	}

	// Should have envFrom blocks injected
	if !strings.Contains(result, "kubernetes-services-endpoint") {
		t.Error("Expected 'kubernetes-services-endpoint' to be injected")
	}

	// Should preserve existing resources (not override)
	if !strings.Contains(result, `cpu: "1"`) {
		t.Error("Expected existing resources to be preserved")
	}

	// Should NOT have our resources block (since one already exists)
	if strings.Contains(result, `cpu: "2"`) {
		t.Error("Should not override existing resources block")
	}

	// Count resources: occurrences - should only be 1
	count := strings.Count(result, "resources:")
	if count != 1 {
		t.Errorf("Expected exactly 1 'resources:' block, got %d", count)
	}

	t.Log("✓ Existing resources test passed - resources preserved, envFrom injected")

	lines := strings.Split(result, "\n")
	t.Log("\nModified content:")
	for i, line := range lines {
		t.Logf("%3d: %s", i+1, line)
	}
}

func TestInjectInlineContainerSpec_BothEnvFromAndResources(t *testing.T) {
	input := `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: test-container
          image: nginx:latest
          envFrom:
          - secretRef:
              name: my-secret
          resources:
            limits:
              cpu: "1"
          env:
          - name: TEST_VAR
            value: test`

	result, err := injectInlineContainerSpec(input, "inject-blocks.yaml", "")
	if err != nil {
		t.Fatalf("injectInlineContainerSpec failed: %v", err)
	}

	// Should append our configMaps to existing envFrom
	if !strings.Contains(result, "kubernetes-services-endpoint") {
		t.Error("Expected 'kubernetes-services-endpoint' to be appended")
	}

	// Note: inject-blocks.yaml only has kubernetes-services-endpoint envFrom entry
	// (common-config and common-secrets were removed to simplify test setup)

	// Should preserve existing secretRef
	if !strings.Contains(result, "my-secret") {
		t.Error("Expected existing secretRef to be preserved")
	}

	// Should preserve existing resources
	if !strings.Contains(result, `cpu: "1"`) {
		t.Error("Expected existing resources to be preserved")
	}

	// Should NOT inject our resources
	if strings.Contains(result, `cpu: "2"`) {
		t.Error("Should not inject resources when one already exists")
	}

	t.Log("✓ Both existing blocks test passed")

	lines := strings.Split(result, "\n")
	t.Log("\nModified content:")
	for i, line := range lines {
		t.Logf("%3d: %s", i+1, line)
	}
}
