package helm_parser

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/distribution/reference"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	_ "helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
	"helm.sh/helm/v3/pkg/release"
)

var (
	// RegistryAttrs is a list of common Helm chart value keys that specify container image
	// repositories in the chart values.yaml
	RegistryAttrs = []string{"hub", "registry", "repository"}
	Logger        = logrus.New()
	// knownWrapperKeys is a list of known wrapper keys used by various Helm charts
	// that should be treated as virtual roots for value injection
	KnownWrapperKeys = []string{
		"_internal_defaults_do_not_set", // Istio charts
	}
)

func LoadValues(chartPath string) (map[interface{}]interface{}, error) {
	if _, err := os.Stat(chartPath); os.IsNotExist(err) {
		log.Printf("Chart path %s does not exist", chartPath)
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(chartPath, "values.yaml")); os.IsNotExist(err) {
		log.Printf("values.yaml does not exist in chart path %s", chartPath)
		return nil, err
	}
	// Load values.yaml
	valuesFilePath := filepath.Join(chartPath, "values.yaml")
	valuesFile, err := os.ReadFile(valuesFilePath)
	if err != nil {
		log.Printf("Error reading values.yaml: %v", err)
		return nil, err
	}
	//Load as a yaml map
	valuesMap := make(map[interface{}]interface{})
	err = yaml.Unmarshal(valuesFile, &valuesMap)
	if err != nil {
		log.Printf("Error unmarshalling values.yaml: %v", err)
		return nil, err
	}

	return valuesMap, nil
}

func checkRegistryAttr(key interface{}) bool {
	// Check if the key is in the list of known registry attributes we defined
	for _, attr := range RegistryAttrs {
		if key == attr {
			return true
		}
	}
	return false
}

// renderChartLocal renders a chart completely locally using Helm's engine and chartutil
// This does not contact a Kubernetes API server.
func renderChartLocal(chartPath string, values map[string]interface{}) (*release.Release, error) {
	chart, err := loader.Load(chartPath)
	if err != nil {
		Logger.Errorf("chart loader.Load failed: %v", err)
		return nil, err
	}

	// Prepare release options for templating, use default name and namespace
	relOpts := chartutil.ReleaseOptions{
		Name:      "test",
		Namespace: "default",
	}

	// Create render values (chart values merged with release options and capabilities)
	//type Capabilities struct {
	//     KubeVersion KubeVersion      // Kubernetes version info
	//     APIVersions VersionSet       // Available API versions (e.g., "apps/v1", "batch/v1")
	//     HelmVersion HelmVersion      // Helm version info
	// }
	//
	renderValues, err := chartutil.ToRenderValues(chart, values, relOpts, chartutil.DefaultCapabilities)
	if err != nil {
		Logger.Errorf("chartutil.ToRenderValues failed: %v\n values: %s", err, values)
		return nil, err
	}
	// Render templates locally using Helm's engine
	eng := engine.Engine{}
	rendered, err := eng.Render(chart, renderValues)
	if err != nil {
		return nil, err
	}

	// Combine rendered templates into a single manifest string (similar to Helm install dry-run)
	var sb strings.Builder
	for _, v := range rendered {
		sb.WriteString(v)
		// ensure YAML separator between resources
		sb.WriteString("\n---\n")
	}

	rel := &release.Release{
		Name:      "test",
		Namespace: "default",
		Manifest:  sb.String(),
		Chart:     chart,
	}

	return rel, nil
}

func convertMapI2MapS(i interface{}) interface{} {
	switch x := i.(type) {
	case map[interface{}]interface{}:
		m2 := make(map[string]interface{})
		for k, v := range x {
			keyStr := fmt.Sprintf("%v", k) // convert key to string
			m2[keyStr] = convertMapI2MapS(v)
		}
		return m2
	case []interface{}:
		for i, v := range x {
			x[i] = convertMapI2MapS(v)
		}
		return x
	default:
		return i
	}
}

// UpdateRegistryInValuesFile updates registry paths (hub, registry, repository) in values.yaml
// while preserving comments, order, and formatting using text-based manipulation
func UpdateRegistryInValuesFile(chartPath string, newRepo string) error {
	valuesPath := filepath.Join(chartPath, "values.yaml")

	// Read the values file
	content, err := os.ReadFile(valuesPath)
	if err != nil {
		return fmt.Errorf("failed to read values.yaml: %v", err)
	}

	// Parse newRepo to get domain and path
	newRegNamed, err := reference.ParseNormalizedNamed(newRepo)
	if err != nil {
		return fmt.Errorf("error parsing new repo reference %s: %v", newRepo, err)
	}
	newRegDomain := reference.Domain(newRegNamed)
	newRegPath := reference.Path(newRegNamed)
	//DEBUG
	Logger.Infof("Registry path to inject: %s, path: %s", newRegDomain, newRegPath)

	modifiedContent, modified := replaceRegistryInText(string(content), newRegDomain, newRegPath)

	// Write back to values.yaml file
	if err := os.WriteFile(valuesPath, []byte(modifiedContent), 0644); err != nil {
		return fmt.Errorf("failed to write updated values.yaml: %v", err)
	}
	if modified {
		Logger.Infof("Updated registry paths in %s", valuesPath)
	}
	return nil
}

// replaceRegistryInText updates registry attribute values in YAML text while preserving format
func replaceRegistryInText(content string, newRegDomain string, newRegPath string) (string, bool) {
	lines := strings.Split(content, "\n")
	// result will hold modified lines to be written back to values.yaml
	var result []string
	var regpathKey bool

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		regpathKey = false

		// Skip empty lines and comments
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			result = append(result, line)
			continue
		}

		// Check if this line contains a registry attribute
		if strings.Contains(trimmed, ":") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])

				// Check if key is a known registry attribute
				if checkRegistryAttr(key) && value != "" && value != `""` {
					regpathKey = true
					// Remove quotes if present
					value = strings.Trim(value, `"`)

					// Parse targget registry value
					targetPrefix := path.Join(newRegDomain, newRegPath)
					// Parse existing registry value
					regNamed, err := reference.ParseNormalizedNamed(value)
					if err != nil {
						Logger.Warnf("Could not parse registry value %s: %v", value, err)
						result = append(result, line)
						continue
					}
					//Check if we are already using the target registry
					Logger.Infof("Checking existing registry value %s against target prefix %s", value, targetPrefix)
					if strings.HasPrefix(value, targetPrefix) {
						Logger.Infof("Skipping %s - already using target registry %s", key, targetPrefix)
						result = append(result, line)
						continue
					}
					// Extract existing registry components
					regPath := reference.Path(regNamed)
					// Remove "library/" prefix for Docker Hub official images
					regPath = strings.TrimPrefix(regPath, "library/")
					regDomain := reference.Domain(regNamed)

					// Build new registry value
					var newRepoJoined string
					if regDomain != newRegDomain {
						newRepoJoined = newRegDomain
					} else {
						newRepoJoined = regDomain
					}

					if regPath != newRegPath {
						// Maintain compatibility with artifactory repo structures
						newRepoJoined = path.Join(newRepoJoined, newRegPath, regDomain, regPath)
					} else {
						newRepoJoined = path.Join(newRepoJoined, regPath)
					}

					Logger.Infof("Updating %s from %s to %s", key, value, newRepoJoined)

					// Reconstruct the line preserving indentation
					indent := GetIndentation(line)
					newLine := strings.Repeat(" ", indent) + key + ": " + newRepoJoined
					result = append(result, newLine)
					continue
				}
			}
		}

		result = append(result, line)
	}
	if !regpathKey {
		Logger.Infof("No registry attribute keys found to update in values.yaml")
	}

	return strings.Join(result, "\n"), regpathKey
}

// splitDocuments splits a YAML manifest into documents using lines that are exactly
// '---' or '...' (allowing leading/trailing whitespace) as boundaries. This is
// more robust than a simple string split since it handles CRLF and variations.
func splitDocuments(manifest string) []string {
	var docs []string
	// create a scanner to read the manifest line by line
	s := bufio.NewScanner(strings.NewReader(manifest))
	// write lines to a buffer until we hit a document separator
	// then save the buffer as a document in the docs slice and reset the buffer
	var sb strings.Builder
	for s.Scan() {
		line := s.Text()
		trim := strings.TrimSpace(line)
		if trim == "---" || trim == "..." {
			part := strings.TrimSpace(sb.String())
			if part != "" {
				docs = append(docs, part)
			}
			sb.Reset()
			continue
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	// append final and error check
	if err := s.Err(); err != nil {
		// If scanning fails, fall back to whole manifest as one doc
		return []string{manifest}
	}
	// we need handle the last document after the loop exists we won't see a separator --- or ... for the last doc
	last := strings.TrimSpace(sb.String())
	if last != "" {
		docs = append(docs, last)
	}
	return docs
}

func ProcessChart(chartPath string, localRepo string, customYaml string, criticalDs bool, controlPlane bool, systemCritical string, dryRun bool, verbose bool) error {
	// Verify if the customYaml file exists
	if _, err := os.Stat(customYaml); os.IsNotExist(err) {
		Logger.Errorf("Custom YAML file %s does not exist: %v", customYaml, err)
		return err
	}

	// Backup values.yaml before modifying
	if err := backupValuesFile(chartPath); err != nil {
		Logger.Errorf("failed to backup values.yaml: %v", err)
		return err
	}

	// First load values.yaml from chart
	values, err := LoadValues(chartPath)
	if err != nil {
		Logger.Fatalf("failed to load values: %v", err)
		return err
	}
	// Next update the registry names in values to our localRepo and render the chart
	err = UpdateRegistryInValuesFile(chartPath, localRepo)
	if err != nil {
		Logger.Fatalf("failed to update registry name: %v", err)
		return err
	}
	// After updating values.yaml, render the chart locally with updated values
	rel, err := renderChartFromValues(chartPath)
	if err != nil {
		Logger.Errorf("failed to render chart from updated values: %v", err)
		return err
	}
	// Parse rendered manifest and extract images from pod specs so that we can check if they exist
	images, err := ExtractImagesFromManifest(rel.Manifest)
	if err != nil {
		Logger.Errorf("failed to extract images from manifest: %v", err)
		return err
	}
	Logger.Infof("rendered images:")
	for _, img := range images {
		Logger.Infof("%s", img)
	}
	// Check if images exist in our registry
	imageExistMap, err := CheckImagesExist(context.Background(), images, "", "")
	if err != nil {
		Logger.Errorf("failed to check images existence: %v", err)
	}
	// Log missing images
	failFatal := false

	for _, img := range images {
		if exists, ok := imageExistMap[img]; ok {
			if !exists {
				Logger.Errorf("Image does not exist in registry: %s", img)
				failFatal = true
			} else {
				// DEBUG
				Logger.Infof("Image exists in registry: %s", img)
			}
		}
	}
	if failFatal {
		if !dryRun {
			return fmt.Errorf("one or more images do not exist in registry")
		}
		Logger.Errorf("one or more images do not exist in registry")
	}
	// Next we process the chart teamplates to inject other inline injector blocks
	// Process templates to inject inline injector container spec
	err = ProcessTemplates(chartPath, values, customYaml, criticalDs, controlPlane, systemCritical)
	if err != nil {
		Logger.Errorf("failed to process templates: %v", err)
		return err
	}

	// Validate by rendering the chart again after injection
	// Render the chart locally with updated values
	relUpdated, err := renderChartFromValues(chartPath)
	if err != nil {
		Logger.Errorf("failed to render chart from updated values: %v", err)
		return err
	}

	if verbose {
		Logger.Infof("Rendered manifest after injection:\n%s", relUpdated.Manifest)
	}

	return nil
}

func backupValuesFile(chartPath string) error {
	valuesPath := filepath.Join(chartPath, "values.yaml")
	backupPath := valuesPath + ".backup"

	if _, err := os.Stat(valuesPath); os.IsNotExist(err) {
		return fmt.Errorf("values.yaml does not exist at %s", valuesPath)
	}

	if _, err := os.Stat(backupPath); err == nil {
		return nil
	}

	input, err := os.ReadFile(valuesPath)
	if err != nil {
		return fmt.Errorf("failed to read values.yaml for backup: %w", err)
	}

	err = os.WriteFile(backupPath, input, 0644)
	if err != nil {
		return fmt.Errorf("failed to write values.yaml backup: %w", err)
	}

	Logger.Infof("Backed up values.yaml to %s", backupPath)
	return nil
}

func restoreValuesFile(chartPath string) error {
	valuesPath := filepath.Join(chartPath, "values.yaml")
	backupPath := valuesPath + ".backup"

	input, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("failed to read values.yaml backup: %w", err)
	}

	err = os.WriteFile(valuesPath, input, 0644)
	if err != nil {
		return fmt.Errorf("failed to restore values.yaml from backup: %w", err)
	}

	Logger.Infof("Restored values.yaml from %s", backupPath)
	return nil
}
