package helm_parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectValueReferences(t *testing.T) {
	template := `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      {{- with .Values.tolerations }}
      tolerations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.webhook.affinity }}
      affinity:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      containers:
        - name: test`

	refs := DetectValueReferences(template)

	// Should find both tolerations and webhook.affinity
	if len(refs) != 2 {
		t.Errorf("Expected 2 references, got %d", len(refs))
	}

	// Check tolerations reference
	foundTolerations := false
	foundWebhookAffinity := false

	for _, ref := range refs {
		if len(ref.Path) == 1 && ref.Path[0] == "tolerations" && ref.Key == "tolerations" {
			foundTolerations = true
		}
		if len(ref.Path) == 2 && ref.Path[0] == "webhook" && ref.Path[1] == "affinity" && ref.Key == "affinity" {
			foundWebhookAffinity = true
		}
	}

	if !foundTolerations {
		t.Error("Expected to find tolerations reference")
	}
	if !foundWebhookAffinity {
		t.Error("Expected to find webhook.affinity reference")
	}

	t.Log("✓ Value reference detection test passed")
}

func TestInjectIntoValuesFile(t *testing.T) {
	// Create a temporary values file
	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "values.yaml")

	// Create a sample values file with both root and nested structures
	valuesContent := `# Root level config
tolerations: []
affinity: {}

# Webhook specific config
webhook:
  enabled: true
  tolerations: []
  affinity: {}
  replicaCount: 1

# Other config
someOtherKey: value
`

	if err := os.WriteFile(valuesPath, []byte(valuesContent), 0644); err != nil {
		t.Fatalf("Failed to create test values file: %v", err)
	}

	// Load blocks
	blocks, err := loadInjectorBlocks("inject-blocks.yaml", "")
	if err != nil {
		t.Fatalf("Failed to load blocks: %v", err)
	}

	// Define references to inject
	refs := []ValueReference{
		{Path: []string{"tolerations"}, Key: "tolerations"},
		{Path: []string{"webhook", "tolerations"}, Key: "tolerations"},
		{Path: []string{"affinity"}, Key: "affinity"},
		{Path: []string{"webhook", "affinity"}, Key: "affinity"},
	}

	// Inject into values
	if err := InjectIntoValuesFile(tmpDir, blocks, refs, false, false); err != nil {
		t.Fatalf("InjectIntoValuesFile failed: %v", err)
	}

	// Read the modified file
	modified, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("Failed to read modified values file: %v", err)
	}

	modifiedStr := string(modified)

	// Verify root level tolerations were injected
	if !strings.Contains(modifiedStr, "key: addons.kaas.bloomberg.com/unavailable") {
		t.Error("Expected root level toleration to be injected")
	}

	// Verify root level affinity was injected
	if !strings.Contains(modifiedStr, "nodeAffinity:") {
		t.Error("Expected root level affinity to be injected")
	}

	// Verify webhook tolerations were injected (look for it under webhook: section)
	lines := strings.Split(modifiedStr, "\n")
	inWebhookSection := false
	foundWebhookTolerations := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "webhook:" {
			inWebhookSection = true
		}
		if inWebhookSection && strings.Contains(trimmed, "key: addons.kaas.bloomberg.com/unavailable") {
			foundWebhookTolerations = true
			break
		}
		// Exit webhook section when we hit another root key
		if inWebhookSection && getIndentation(line) == 0 && trimmed != "" && trimmed != "webhook:" {
			break
		}
	}

	if !foundWebhookTolerations {
		t.Error("Expected webhook tolerations to be injected")
	}

	t.Log("✓ Values file injection test passed")

	t.Log("\nModified values.yaml:")
	for i, line := range lines {
		if i < 50 { // Print first 50 lines
			t.Logf("%3d: %s", i+1, line)
		}
	}
}
