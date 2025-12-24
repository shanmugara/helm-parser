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
	blocks, err := loadInjectorBlocks("inject-blocks.yaml")
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

func TestInjectNewValues(t *testing.T) {
	// Create a temporary values file
	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "values.yaml")

	// Create a sample values file with existing content
	valuesContent := `# Existing config
replicaCount: 1
image:
  repository: nginx
  tag: latest

# Existing somekey value (will be overwritten)
somekey: oldvalue
`

	if err := os.WriteFile(valuesPath, []byte(valuesContent), 0644); err != nil {
		t.Fatalf("Failed to create test values file: %v", err)
	}

	// Load blocks
	blocks, err := loadInjectorBlocks("inject-blocks.yaml")
	if err != nil {
		t.Fatalf("Failed to load blocks: %v", err)
	}

	// Inject with no referenced paths, only newValues
	refs := []ValueReference{}
	if err := InjectIntoValuesFile(tmpDir, blocks, refs, false, false); err != nil {
		t.Fatalf("InjectIntoValuesFile failed: %v", err)
	}

	// Read the modified file
	modified, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("Failed to read modified values file: %v", err)
	}

	modifiedStr := string(modified)
	lines := strings.Split(modifiedStr, "\n")

	// Verify somekey was overwritten (should be "somevalue", not "oldvalue")
	foundSomekey := false
	somekeyValue := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "somekey:") {
			foundSomekey = true
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				somekeyValue = strings.TrimSpace(parts[1])
			}
			break
		}
	}

	if !foundSomekey {
		t.Error("Expected 'somekey' to exist in values.yaml")
	}

	if somekeyValue != "somevalue" {
		t.Errorf("Expected somekey to be 'somevalue' (overwritten), got '%s'", somekeyValue)
	}

	// Verify hostPort was added as a new key
	foundHostPort := false
	foundHostPortEnabled := false
	foundHostPortPorts := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "hostPort:" {
			foundHostPort = true
		}
		if strings.HasPrefix(trimmed, "enabled:") && strings.Contains(line, "false") {
			foundHostPortEnabled = true
		}
		if trimmed == "ports:" {
			foundHostPortPorts = true
		}
	}

	if !foundHostPort {
		t.Error("Expected 'hostPort' to be injected into values.yaml")
	}

	if !foundHostPortEnabled {
		t.Error("Expected 'hostPort.enabled: false' to be injected")
	}

	if !foundHostPortPorts {
		t.Error("Expected 'hostPort.ports' to be injected")
	}

	// Verify the structure is correct - check for http2 and https ports
	if !strings.Contains(modifiedStr, "http2: 80") {
		t.Error("Expected 'http2: 80' in hostPort.ports")
	}

	if !strings.Contains(modifiedStr, "https: 443") {
		t.Error("Expected 'https: 443' in hostPort.ports")
	}

	t.Log("✓ New values injection test passed")

	t.Log("\nModified values.yaml:")
	for i, line := range lines {
		t.Logf("%3d: %s", i+1, line)
	}
}

func TestInjectNewValuesWithWrapper(t *testing.T) {
	// Create a temporary values file with a wrapper pattern (like Istio)
	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "values.yaml")

	// Create a values file with _internal_defaults_do_not_set wrapper
	valuesContent := `_internal_defaults_do_not_set:
  # Existing config
  replicaCount: 1
  image:
    repository: nginx
    tag: latest

  # Existing somekey value (will be overwritten)
  somekey: oldvalue
`

	if err := os.WriteFile(valuesPath, []byte(valuesContent), 0644); err != nil {
		t.Fatalf("Failed to create test values file: %v", err)
	}

	// Load blocks
	blocks, err := loadInjectorBlocks("inject-blocks.yaml")
	if err != nil {
		t.Fatalf("Failed to load blocks: %v", err)
	}

	// Inject with no referenced paths, only newValues
	refs := []ValueReference{}
	if err := InjectIntoValuesFile(tmpDir, blocks, refs, false, false); err != nil {
		t.Fatalf("InjectIntoValuesFile failed: %v", err)
	}

	// Read the modified file
	modified, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("Failed to read modified values file: %v", err)
	}

	modifiedStr := string(modified)
	lines := strings.Split(modifiedStr, "\n")

	// Verify the wrapper still exists
	if !strings.Contains(modifiedStr, "_internal_defaults_do_not_set:") {
		t.Error("Expected wrapper key '_internal_defaults_do_not_set' to exist")
	}

	// Verify somekey was overwritten and is properly indented inside the wrapper
	foundSomekey := false
	somekeyValue := ""
	somekeyIndent := -1
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "somekey:") {
			foundSomekey = true
			somekeyIndent = getIndentation(line)
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				somekeyValue = strings.TrimSpace(parts[1])
			}
			break
		}
	}

	if !foundSomekey {
		t.Error("Expected 'somekey' to exist in values.yaml")
	}

	if somekeyValue != "somevalue" {
		t.Errorf("Expected somekey to be 'somevalue' (overwritten), got '%s'", somekeyValue)
	}

	if somekeyIndent != 2 {
		t.Errorf("Expected somekey to be indented at 2 spaces (inside wrapper), got %d", somekeyIndent)
	}

	// Verify hostPort was added inside the wrapper with proper indentation
	foundHostPort := false
	hostPortIndent := -1
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "hostPort:" {
			foundHostPort = true
			hostPortIndent = getIndentation(line)
			break
		}
	}

	if !foundHostPort {
		t.Error("Expected 'hostPort' to be injected into values.yaml")
	}

	if hostPortIndent != 2 {
		t.Errorf("Expected hostPort to be indented at 2 spaces (inside wrapper), got %d", hostPortIndent)
	}

	// Verify nested properties are properly indented (4 spaces for enabled, 4 for ports, 6 for http2/https)
	if !strings.Contains(modifiedStr, "    enabled: false") {
		t.Error("Expected 'enabled' to be indented at 4 spaces")
	}

	if !strings.Contains(modifiedStr, "    ports:") {
		t.Error("Expected 'ports' to be indented at 4 spaces")
	}

	if !strings.Contains(modifiedStr, "      http2: 80") {
		t.Error("Expected 'http2' to be indented at 6 spaces")
	}

	if !strings.Contains(modifiedStr, "      https: 443") {
		t.Error("Expected 'https' to be indented at 6 spaces")
	}

	t.Log("✓ New values injection with wrapper pattern test passed")

	t.Log("\nModified values.yaml:")
	for i, line := range lines {
		t.Logf("%3d: %s", i+1, line)
	}
}

func TestInjectNewValuesIdempotent(t *testing.T) {
	// Test that running injection twice produces the same result (idempotent)
	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "values.yaml")

	// Create a sample values file
	valuesContent := `# Existing config
replicaCount: 1
image:
  repository: nginx
  tag: latest
`

	if err := os.WriteFile(valuesPath, []byte(valuesContent), 0644); err != nil {
		t.Fatalf("Failed to create test values file: %v", err)
	}

	// Load blocks
	blocks, err := loadInjectorBlocks("inject-blocks.yaml")
	if err != nil {
		t.Fatalf("Failed to load blocks: %v", err)
	}

	// Inject with no referenced paths, only newValues - FIRST TIME
	refs := []ValueReference{}
	if err := InjectIntoValuesFile(tmpDir, blocks, refs, false, false); err != nil {
		t.Fatalf("First InjectIntoValuesFile failed: %v", err)
	}

	// Read the modified file after first injection
	firstRun, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("Failed to read modified values file after first run: %v", err)
	}

	// Inject again - SECOND TIME
	if err := InjectIntoValuesFile(tmpDir, blocks, refs, false, false); err != nil {
		t.Fatalf("Second InjectIntoValuesFile failed: %v", err)
	}

	// Read the modified file after second injection
	secondRun, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("Failed to read modified values file after second run: %v", err)
	}

	// Compare - they should be identical
	if string(firstRun) != string(secondRun) {
		t.Error("Running injection twice produced different results - not idempotent!")

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

	// Verify hostPort is present only once
	hostPortCount := strings.Count(string(secondRun), "hostPort:")
	if hostPortCount != 1 {
		t.Errorf("Expected 'hostPort:' to appear exactly once, found %d occurrences", hostPortCount)
	}

	// Verify enabled appears only once (under hostPort)
	enabledCount := strings.Count(string(secondRun), "enabled: false")
	if enabledCount != 1 {
		t.Errorf("Expected 'enabled: false' to appear exactly once, found %d occurrences", enabledCount)
	}

	t.Log("✓ Idempotent injection test passed")

	t.Log("\nFinal values.yaml:")
	for i, line := range strings.Split(string(secondRun), "\n") {
		t.Logf("%3d: %s", i+1, line)
	}
}

func TestDeepMergeNewValues(t *testing.T) {
	// Create a temporary values file with existing nested structure
	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "values.yaml")

	initialValues := `# Test values file
app:
  mode: dashboard
  image:
    repository: test/image
    tag: v1.0.0
    pullPolicy: IfNotPresent
  security:
    csrfKey: existing-key
    securityContext:
      runAsNonRoot: true
      seccompProfile:
        type: RuntimeDefault
    containerSecurityContext:
      allowPrivilegeEscalation: false
      readOnlyRootFilesystem: true
  scheduling:
    nodeSelector:
      disk: ssd
other:
  existingKey: existingValue
`

	if err := os.WriteFile(valuesPath, []byte(initialValues), 0644); err != nil {
		t.Fatalf("Failed to create test values file: %v", err)
	}

	// Create blocks with newValues that should deep merge
	blocks := InjectorBlocks{
		"newValues": []string{
			`app:
  securityContext:
    runAsUser: 1001
    runAsGroup: 2001
  networking:
    port: 8080
`,
			`other:
  newKey: newValue
`,
		},
	}

	// No referenced paths - pure newValues injection
	var referencedPaths []ValueReference

	err := InjectIntoValuesFile(tmpDir, blocks, referencedPaths, false, false)
	if err != nil {
		t.Fatalf("Failed to inject into values file: %v", err)
	}

	// Read the result
	result, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("Failed to read result: %v", err)
	}

	resultStr := string(result)

	// Verify existing app keys are preserved
	if !strings.Contains(resultStr, "mode: dashboard") {
		t.Error("Expected 'mode: dashboard' to be preserved")
	}
	if !strings.Contains(resultStr, "repository: test/image") {
		t.Error("Expected 'repository: test/image' to be preserved")
	}
	if !strings.Contains(resultStr, "tag: v1.0.0") {
		t.Error("Expected 'tag: v1.0.0' to be preserved")
	}
	if !strings.Contains(resultStr, "csrfKey: existing-key") {
		t.Error("Expected 'csrfKey: existing-key' to be preserved")
	}
	if !strings.Contains(resultStr, "runAsNonRoot: true") {
		t.Error("Expected 'runAsNonRoot: true' to be preserved")
	}
	if !strings.Contains(resultStr, "allowPrivilegeEscalation: false") {
		t.Error("Expected 'allowPrivilegeEscalation: false' to be preserved")
	}
	if !strings.Contains(resultStr, "disk: ssd") {
		t.Error("Expected 'disk: ssd' to be preserved")
	}

	// Verify new values were added
	if !strings.Contains(resultStr, "runAsUser: 1001") {
		t.Error("Expected new 'runAsUser: 1001' to be added")
	}
	if !strings.Contains(resultStr, "runAsGroup: 2001") {
		t.Error("Expected new 'runAsGroup: 2001' to be added")
	}
	if !strings.Contains(resultStr, "networking:") {
		t.Error("Expected new 'networking' section to be added")
	}
	if !strings.Contains(resultStr, "port: 8080") {
		t.Error("Expected new 'port: 8080' to be added")
	}

	// Verify other key was merged
	if !strings.Contains(resultStr, "existingKey: existingValue") {
		t.Error("Expected 'existingKey: existingValue' to be preserved")
	}
	if !strings.Contains(resultStr, "newKey: newValue") {
		t.Error("Expected new 'newKey: newValue' to be added")
	}

	t.Log("✓ Deep merge test passed")

	t.Log("\nFinal merged values.yaml:")
	for i, line := range strings.Split(resultStr, "\n") {
		t.Logf("%3d: %s", i+1, line)
	}
}
