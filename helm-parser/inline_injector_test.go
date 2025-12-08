package helm_parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessTemplates(t *testing.T) {
	// Use the cert-manager chart that's already in the workspace
	chartDir := "../cert-manager"

	// Create backups before modifying
	valuesPath := filepath.Join(chartDir, "values.yaml")
	valuesBackupPath := valuesPath + ".testbackup"
	deploymentPath := filepath.Join(chartDir, "templates", "deployment.yaml")
	deploymentBackupPath := deploymentPath + ".testbackup"

	// Read original content
	originalValuesContent, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("Failed to read values.yaml: %v", err)
	}
	originalDeploymentContent, err := os.ReadFile(deploymentPath)
	if err != nil {
		t.Fatalf("Failed to read deployment.yaml: %v", err)
	}

	// Write backups
	if err := os.WriteFile(valuesBackupPath, originalValuesContent, 0644); err != nil {
		t.Fatalf("Failed to create values backup: %v", err)
	}
	if err := os.WriteFile(deploymentBackupPath, originalDeploymentContent, 0644); err != nil {
		t.Fatalf("Failed to create deployment backup: %v", err)
	}

	// Ensure backups are restored after test
	defer func() {
		os.WriteFile(valuesPath, originalValuesContent, 0644)
		os.WriteFile(deploymentPath, originalDeploymentContent, 0644)
		os.Remove(valuesBackupPath)
		os.Remove(deploymentBackupPath)
	}()

	// Run ProcessTemplates
	values, err := LoadValues(chartDir)
	if err != nil {
		t.Fatalf("Failed to load values.yaml: %v", err)
	}

	err = ProcessTemplates(chartDir, values, "inject-blocks.yaml", false, false)
	if err != nil {
		t.Fatalf("ProcessTemplates failed: %v", err)
	}

	// Read the modified values.yaml (since cert-manager uses .Values, blocks go into values.yaml)
	modifiedValuesContent, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("Failed to read modified values.yaml: %v", err)
	}

	modifiedValuesStr := string(modifiedValuesContent)

	// Check if resources block was injected into values.yaml (cert-manager uses .Values.resources)
	if !strings.Contains(modifiedValuesStr, "cpu: 250m") {
		t.Error("Expected resources block to be injected in values.yaml")
	}

	if !strings.Contains(modifiedValuesStr, "memory: 512Mi") {
		t.Error("Expected resources block to be injected in values.yaml")
	}

	// Deployment template should NOT be modified (uses .Values references)
	modifiedDeploymentContent, err := os.ReadFile(deploymentPath)
	if err != nil {
		t.Fatalf("Failed to read deployment.yaml: %v", err)
	}
	
	if string(modifiedDeploymentContent) != string(originalDeploymentContent) {
		t.Error("deployment.yaml should not be modified when using .Values references")
	}

	t.Log("✓ ProcessTemplates correctly injected into values.yaml and skipped template modification")
}

func TestInjectInlineContainerSpec(t *testing.T) {
	input := `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: test-container
          image: nginx:latest
          imagePullPolicy: IfNotPresent
          ports:
          - containerPort: 80
          env:
          - name: TEST_VAR
            value: test`

	result, err := injectInlineContainerSpec(input, "inject-blocks.yaml")
	if err != nil {
		t.Fatalf("injectInlineContainerSpec failed: %v", err)
	}

	if !strings.Contains(result, "envFrom:") {
		t.Error("Expected 'envFrom:' to be present")
	}

	if !strings.Contains(result, "kubernetes-services-endpoint") {
		t.Error("Expected 'kubernetes-services-endpoint' to be present")
	}

	// Check that envFrom comes before env
	envFromIndex := strings.Index(result, "envFrom:")
	envIndex := strings.Index(result, "env:")

	if envFromIndex >= envIndex {
		t.Error("Expected 'envFrom:' to appear before 'env:'")
	}

	t.Log("✓ Inline injection test passed")

	// Print line by line with line numbers for debugging
	lines := strings.Split(result, "\n")
	t.Log("\nModified content (line by line):")
	for i, line := range lines {
		t.Logf("%3d: %s", i+1, line)
	}
}

func TestInjectInlineContainerSpec_ExistingEnvFrom(t *testing.T) {
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
          env:
          - name: TEST_VAR
            value: test`

	result, err := injectInlineContainerSpec(input, "inject-blocks.yaml")
	if err != nil {
		t.Fatalf("injectInlineContainerSpec failed: %v", err)
	}

	// Should have both secretRef and our configMapRef
	if !strings.Contains(result, "secretRef:") {
		t.Error("Expected existing 'secretRef:' to be preserved")
	}

	if !strings.Contains(result, "configMapRef:") {
		t.Error("Expected 'configMapRef:' to be added")
	}

	if !strings.Contains(result, "kubernetes-services-endpoint") {
		t.Error("Expected 'kubernetes-services-endpoint' to be present")
	}

	t.Log("✓ Existing envFrom test passed - both items present")

	lines := strings.Split(result, "\n")
	t.Log("\nModified content:")
	for i, line := range lines {
		t.Logf("%3d: %s", i+1, line)
	}
}

func TestInjectInlineContainerSpec_Idempotent(t *testing.T) {
	input := `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: test-container
          image: nginx:latest
          envFrom:
          - configMapRef:
              name: kubernetes-services-endpoint
          env:
          - name: TEST_VAR
            value: test`

	result, err := injectInlineContainerSpec(input, "inject-blocks.yaml")
	if err != nil {
		t.Fatalf("injectInlineContainerSpec failed: %v", err)
	}

	// Count occurrences of our configMap name
	count := strings.Count(result, "kubernetes-services-endpoint")
	if count != 1 {
		t.Errorf("Expected exactly 1 occurrence of 'kubernetes-services-endpoint', got %d", count)
		t.Log("Result after first injection:")
		lines := strings.Split(result, "\n")
		for i, line := range lines {
			t.Logf("%3d: %s", i+1, line)
		}
	}

	// Run it again - should still be idempotent
	result2, err := injectInlineContainerSpec(result, "inject-blocks.yaml")
	if err != nil {
		t.Fatalf("Second injectInlineContainerSpec failed: %v", err)
	}

	count2 := strings.Count(result2, "kubernetes-services-endpoint")
	if count2 != 1 {
		t.Errorf("After second injection, expected exactly 1 occurrence of 'kubernetes-services-endpoint', got %d", count2)
	}

	t.Log("✓ Idempotent test passed - no duplication")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
