package helm_parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCustomFileMods(t *testing.T) {
	mods, err := loadCustomFileMods("inject-blocks.yaml")
	if err != nil {
		t.Fatalf("Failed to load custom file mods: %v", err)
	}

	if len(mods) == 0 {
		t.Fatal("Expected at least one custom file mod, got none")
	}

	// Verify the structure
	firstMod := mods[0]
	if firstMod.File != "templates/deployment.yaml" {
		t.Errorf("Expected file 'templates/deployment.yaml', got '%s'", firstMod.File)
	}

	if len(firstMod.Modifications) == 0 {
		t.Fatal("Expected at least one modification, got none")
	}

	modification := firstMod.Modifications[0]
	if modification.Name != "Add hostPort support" {
		t.Errorf("Expected name 'Add hostPort support', got '%s'", modification.Name)
	}

	if len(modification.AnchorLines) != 3 {
		t.Errorf("Expected 3 anchor lines, got %d", len(modification.AnchorLines))
	}

	if modification.Position != "after" {
		t.Errorf("Expected position 'after', got '%s'", modification.Position)
	}

	if !strings.Contains(modification.Block, "{{- range .Values.service.ports }}") {
		t.Error("Expected block to contain '{{- range .Values.service.ports }}'")
	}

	t.Log("✓ Custom file mods loaded successfully")
}

func TestApplyCustomFileMods(t *testing.T) {
	// Create a temporary directory with a test template
	tmpDir := t.TempDir()
	templatesDir := filepath.Join(tmpDir, "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatalf("Failed to create templates directory: %v", err)
	}

	// Create a test deployment.yaml without the hostPort block
	deploymentContent := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-gateway
spec:
  template:
    spec:
      containers:
        - name: istio-proxy
          image: auto
          ports:
          - containerPort: 15090
            protocol: TCP
            name: http-envoy-prom
          resources:
            limits:
              memory: "128Mi"
`

	deploymentPath := filepath.Join(templatesDir, "deployment.yaml")
	if err := os.WriteFile(deploymentPath, []byte(deploymentContent), 0644); err != nil {
		t.Fatalf("Failed to write test deployment file: %v", err)
	}

	// Apply custom file mods
	if err := ApplyCustomTemplateMods(tmpDir, "inject-blocks.yaml"); err != nil {
		t.Fatalf("Failed to apply custom file mods: %v", err)
	}

	// Read the modified file
	modified, err := os.ReadFile(deploymentPath)
	if err != nil {
		t.Fatalf("Failed to read modified deployment file: %v", err)
	}

	modifiedStr := string(modified)

	// Verify the block was injected
	if !strings.Contains(modifiedStr, "{{- range .Values.service.ports }}") {
		t.Error("Expected modified file to contain '{{- range .Values.service.ports }}'")
	}

	if !strings.Contains(modifiedStr, "{{- if $.Values.hostPort.enabled }}") {
		t.Error("Expected modified file to contain hostPort condition")
	}

	if !strings.Contains(modifiedStr, "hostPort: {{ index $.Values.hostPort.ports .name | default .targetPort }}") {
		t.Error("Expected modified file to contain hostPort template")
	}

	// Verify the block was inserted after the anchor
	lines := strings.Split(modifiedStr, "\n")
	foundAnchor := false
	foundBlock := false
	anchorIndex := -1

	for i, line := range lines {
		if strings.TrimSpace(line) == "name: http-envoy-prom" {
			foundAnchor = true
			anchorIndex = i
		}
		if foundAnchor && strings.Contains(line, "{{- range .Values.service.ports }}") {
			foundBlock = true
			if i <= anchorIndex {
				t.Error("Block was not inserted after the anchor")
			}
			break
		}
	}

	if !foundAnchor {
		t.Error("Could not find anchor line in modified file")
	}

	if !foundBlock {
		t.Error("Could not find injected block in modified file")
	}

	t.Log("✓ Custom file modification applied successfully")

	t.Log("\nModified deployment.yaml:")
	for i, line := range lines {
		t.Logf("%3d: %s", i+1, line)
	}
}

func TestApplyCustomFileModsIdempotent(t *testing.T) {
	// Create a temporary directory with a test template
	tmpDir := t.TempDir()
	templatesDir := filepath.Join(tmpDir, "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatalf("Failed to create templates directory: %v", err)
	}

	// Create a test deployment.yaml
	deploymentContent := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-gateway
spec:
  template:
    spec:
      containers:
        - name: istio-proxy
          ports:
          - containerPort: 15090
            protocol: TCP
            name: http-envoy-prom
`

	deploymentPath := filepath.Join(templatesDir, "deployment.yaml")
	if err := os.WriteFile(deploymentPath, []byte(deploymentContent), 0644); err != nil {
		t.Fatalf("Failed to write test deployment file: %v", err)
	}

	// Apply custom file mods - FIRST TIME
	if err := ApplyCustomTemplateMods(tmpDir, "inject-blocks.yaml"); err != nil {
		t.Fatalf("First apply failed: %v", err)
	}

	// Read after first application
	firstRun, err := os.ReadFile(deploymentPath)
	if err != nil {
		t.Fatalf("Failed to read file after first run: %v", err)
	}

	// Apply again - SECOND TIME
	if err := ApplyCustomTemplateMods(tmpDir, "inject-blocks.yaml"); err != nil {
		t.Fatalf("Second apply failed: %v", err)
	}

	// Read after second application
	secondRun, err := os.ReadFile(deploymentPath)
	if err != nil {
		t.Fatalf("Failed to read file after second run: %v", err)
	}

	// Compare - they should be identical
	if string(firstRun) != string(secondRun) {
		t.Error("Applying custom file mods twice produced different results - not idempotent!")

		t.Log("\n=== First run output: ===")
		for i, line := range strings.Split(string(firstRun), "\n") {
			t.Logf("%3d: %s", i+1, line)
		}

		t.Log("\n=== Second run output: ===")
		for i, line := range strings.Split(string(secondRun), "\n") {
			t.Logf("%3d: %s", i+1, line)
		}

		t.FailNow()
	}

	t.Log("✓ Custom file modifications are idempotent")
}
