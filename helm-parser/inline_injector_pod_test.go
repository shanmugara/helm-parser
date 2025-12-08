package helm_parser

import (
	"strings"
	"testing"
)

func TestInjectInlinePodSpec_Basic(t *testing.T) {
	blocks, err := loadInjectorBlocks("inject-blocks.yaml", "")
	if err != nil {
		t.Fatalf("Failed to load blocks: %v", err)
	}

	input := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deployment
spec:
  replicas: 1
  template:
    metadata:
      labels:
        app: test
    spec:
      containers:
        - name: test-container
          image: nginx:latest`

	result, err := injectInlinePodSpec(input, blocks, "Deployment", false, false)
	if err != nil {
		t.Fatalf("injectInlinePodSpec failed: %v", err)
	}

	// Should have tolerations injected
	if !strings.Contains(result, "tolerations:") {
		t.Error("Expected 'tolerations:' to be injected in pod spec")
	}

	// Should have affinity injected
	if !strings.Contains(result, "affinity:") {
		t.Error("Expected 'affinity:' to be injected in pod spec")
	}

	// Check that tolerations comes before containers
	tolerationsIndex := strings.Index(result, "tolerations:")
	containersIndex := strings.Index(result, "containers:")

	if tolerationsIndex >= containersIndex {
		t.Error("Expected 'tolerations:' to appear before 'containers:'")
	}

	t.Log("✓ Basic pod spec injection test passed")

	lines := strings.Split(result, "\n")
	t.Log("\nModified content:")
	for i, line := range lines {
		t.Logf("%3d: %s", i+1, line)
	}
}

func TestInjectInlinePodSpec_ExistingTolerations(t *testing.T) {
	blocks, err := loadInjectorBlocks("inject-blocks.yaml", "")
	if err != nil {
		t.Fatalf("Failed to load blocks: %v", err)
	}

	input := `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      tolerations:
        - key: addons.kaas.bloomberg.com/unavailable
          operator: "Exists"
          effect: NoSchedule
      containers:
        - name: test-container
          image: nginx:latest`

	result, err := injectInlinePodSpec(input, blocks, "Deployment", false, false)
	if err != nil {
		t.Fatalf("injectInlinePodSpec failed: %v", err)
	}

	// Should preserve existing toleration
	if !strings.Contains(result, "addons.kaas.bloomberg.com/unavailable") {
		t.Error("Expected existing toleration to be preserved")
	}

	// Should still inject affinity (not a toleration)
	if !strings.Contains(result, "affinity:") {
		t.Error("Expected 'affinity:' to be injected")
	}

	// Count tolerations: occurrences - should be 1 (idempotent)
	count := strings.Count(result, "tolerations:")
	if count != 1 {
		t.Errorf("Expected exactly 1 'tolerations:' block, got %d", count)
	}

	t.Log("✓ Existing tolerations test passed")

	lines := strings.Split(result, "\n")
	t.Log("\nModified content:")
	for i, line := range lines {
		t.Logf("%3d: %s", i+1, line)
	}
}

func TestInjectInlinePodSpec_Idempotent(t *testing.T) {
	blocks, err := loadInjectorBlocks("inject-blocks.yaml", "")
	if err != nil {
		t.Fatalf("Failed to load blocks: %v", err)
	}

	input := `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: test-container
          image: nginx:latest`

	// First injection
	result, err := injectInlinePodSpec(input, blocks, "Deployment", false, false)
	if err != nil {
		t.Fatalf("First injection failed: %v", err)
	}

	// Second injection
	result2, err := injectInlinePodSpec(result, blocks, "Deployment", false, false)
	if err != nil {
		t.Fatalf("Second injection failed: %v", err)
	}

	// Count key blocks - should be the same after second injection
	tolerations1 := strings.Count(result, "tolerations:")
	tolerations2 := strings.Count(result2, "tolerations:")

	if tolerations1 != tolerations2 {
		t.Errorf("Tolerations count changed from %d to %d - not idempotent", tolerations1, tolerations2)
	}

	affinity1 := strings.Count(result, "affinity:")
	affinity2 := strings.Count(result2, "affinity:")

	if affinity1 != affinity2 {
		t.Errorf("Affinity count changed from %d to %d - not idempotent", affinity1, affinity2)
	}

	t.Log("✓ Idempotent test passed - no duplication")
}

func TestInjectInlinePodSpec_NotUnderTemplate(t *testing.T) {
	blocks, err := loadInjectorBlocks("inject-blocks.yaml", "")
	if err != nil {
		t.Fatalf("Failed to load blocks: %v", err)
	}

	// This has spec: but not under template: (it's at the Deployment level)
	input := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: test
spec:
  replicas: 1
  selector:
    matchLabels:
      app: test
  template:
    metadata:
      labels:
        app: test
    spec:
      containers:
        - name: test-container
          image: nginx:latest`

	result, err := injectInlinePodSpec(input, blocks, "Deployment", false, false)
	if err != nil {
		t.Fatalf("injectInlinePodSpec failed: %v", err)
	}

	// Should have tolerations injected only in the pod spec (under template:)
	lines := strings.Split(result, "\n")
	foundDeploymentSpec := false
	foundTemplateSpec := false
	tolerationsAfterDeploymentSpec := false
	tolerationsAfterTemplateSpec := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if !foundDeploymentSpec && trimmed == "spec:" {
			foundDeploymentSpec = true
			// Check next few lines for tolerations
			for j := i + 1; j < len(lines) && j < i+10; j++ {
				if strings.TrimSpace(lines[j]) == "tolerations:" {
					tolerationsAfterDeploymentSpec = true
					break
				}
				if strings.TrimSpace(lines[j]) == "template:" {
					break
				}
			}
		}

		if foundDeploymentSpec && !foundTemplateSpec && trimmed == "spec:" && i > 5 {
			foundTemplateSpec = true
			// Check next few lines for tolerations
			for j := i + 1; j < len(lines) && j < i+20; j++ {
				if strings.TrimSpace(lines[j]) == "tolerations:" {
					tolerationsAfterTemplateSpec = true
					break
				}
			}
		}
	}

	if tolerationsAfterDeploymentSpec {
		t.Error("Tolerations should NOT be injected after Deployment spec:")
	}

	if !tolerationsAfterTemplateSpec {
		t.Error("Tolerations should be injected after template spec:")
	}

	t.Log("✓ Template spec detection test passed")
}

func TestInjectInlinePodSpec_PodKind(t *testing.T) {
	blocks, err := loadInjectorBlocks("inject-blocks.yaml", "")
	if err != nil {
		t.Fatalf("Failed to load blocks: %v", err)
	}

	// Pod kind has spec: directly under kind, not under template:
	input := `apiVersion: v1
kind: Pod
metadata:
  name: test-pod
spec:
  containers:
    - name: test-container
      image: nginx:latest`

	result, err := injectInlinePodSpec(input, blocks, "Pod", false, false)
	if err != nil {
		t.Fatalf("injectInlinePodSpec failed: %v", err)
	}

	// Should have tolerations injected
	if !strings.Contains(result, "tolerations:") {
		t.Error("Expected 'tolerations:' to be injected in pod spec")
	}

	// Should have affinity injected
	if !strings.Contains(result, "affinity:") {
		t.Error("Expected 'affinity:' to be injected in pod spec")
	}

	// Verify that tolerations is injected under the spec: (not under some other section)
	lines := strings.Split(result, "\n")
	foundPodSpec := false
	tolerationsAfterPodSpec := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if !foundPodSpec && trimmed == "spec:" {
			foundPodSpec = true
			// Check next lines for tolerations
			for j := i + 1; j < len(lines) && j < i+15; j++ {
				if strings.TrimSpace(lines[j]) == "tolerations:" {
					tolerationsAfterPodSpec = true
					break
				}
				// If we hit containers, stop looking
				if strings.TrimSpace(lines[j]) == "containers:" {
					break
				}
			}
			break
		}
	}

	if !tolerationsAfterPodSpec {
		t.Error("Tolerations should be injected directly under Pod spec:")
	}

	t.Log("✓ Pod kind injection test passed")

	t.Log("\nModified content:")
	for i, line := range lines {
		t.Logf("%3d: %s", i+1, line)
	}
}

func TestInjectInlinePodSpec_HelmTemplate(t *testing.T) {
	blocks, err := loadInjectorBlocks("inject-blocks.yaml", "")
	if err != nil {
		t.Fatalf("Failed to load blocks: %v", err)
	}

	// This simulates a real helm template with {{- with .Values.tolerations }}
	input := `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      {{- with .Values.affinity }}
      affinity:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.tolerations }}
      tolerations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      containers:
        - name: test-container
          image: nginx:latest`

	result, err := injectInlinePodSpec(input, blocks, "Deployment", false, false)
	if err != nil {
		t.Fatalf("injectInlinePodSpec failed: %v", err)
	}

	// Should have our tolerations injected before {{- end }}
	if !strings.Contains(result, "key: addons.kaas.bloomberg.com/unavailable") {
		t.Error("Expected our toleration to be injected")
	}

	// Should have affinity injected
	if !strings.Contains(result, "nodeAffinity:") {
		t.Error("Expected 'nodeAffinity:' to be injected")
	}

	lines := strings.Split(result, "\n")

	// Find the tolerations section and verify indentation
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "tolerations:" {
			// Check the next few lines for our injected toleration
			foundToleration := false
			correctIndent := false
			for j := i + 1; j < len(lines) && j < i+10; j++ {
				nextTrimmed := strings.TrimSpace(lines[j])
				if strings.Contains(nextTrimmed, "key: addons.kaas.bloomberg.com/unavailable") {
					foundToleration = true
					// Check indentation - should be 8 spaces (2 for spec indent + 2 for tolerations + 4 for properties)
					indent := len(lines[j]) - len(strings.TrimLeft(lines[j], " "))
					if indent == 8 {
						correctIndent = true
					}
					t.Logf("Found toleration at line %d with indent %d: %s", j+1, indent, lines[j])
					break
				}
			}
			if !foundToleration {
				t.Error("Toleration was not injected in the tolerations block")
			}
			if !correctIndent {
				t.Error("Toleration has incorrect indentation (should be 8 spaces)")
			}
			break
		}
	}

	t.Log("✓ Helm template injection test passed")

	t.Log("\nModified content:")
	for i, line := range lines {
		t.Logf("%3d: %s", i+1, line)
	}
}
