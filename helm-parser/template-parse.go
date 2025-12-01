package helm_parser

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	_ "helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
	"helm.sh/helm/v3/pkg/release"

	regauthn "github.com/google/go-containerregistry/pkg/authn"
	regname "github.com/google/go-containerregistry/pkg/name"
	regremote "github.com/google/go-containerregistry/pkg/v1/remote"
)

var (
	RegistryAttrs = []string{"hub", "registry", "repository"}
	Logger        = logrus.New()
)

func CheckHelmTemplateDir(templatePath string) bool {
	if _, err := os.Stat(templatePath); err != nil {
		if os.IsNotExist(err) {
			Logger.Errorf("template path %s does not exist", templatePath)
			return false
		}
		Logger.Errorf("error accessing template path %s: %v", templatePath, err)
		return false
	}
	return true
}

func GetTemplateFiles(templatePath string) ([]string, error) {
	if !CheckHelmTemplateDir(templatePath) {
		return nil, nil
	}
	var templateFiles []string
	err := filepath.WalkDir(templatePath, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			templateFiles = append(templateFiles, path)
		}
		return nil
	})
	if err != nil {
		Logger.Errorf("error walking template path %s: %v", templatePath, err)
		return nil, err
	}
	return templateFiles, nil
}

func GetTpFilesToParse(templatePath string) ([]string, error) {
	templateFiles, err := GetTemplateFiles(templatePath)
	if err != nil {
		return nil, err
	}
	if len(templateFiles) == 0 {
		Logger.Infof("no template files found in path %s", templatePath)
		return nil, nil
	}
	for _, tp := range templateFiles {
		Logger.Infof("checking template file: %s", tp)

	}
	return GetTemplateFiles(templatePath)
}

func LoadValues(chartPath string) (map[interface{}]interface{}, error) {
	if _, err := os.Stat(chartPath); os.IsNotExist(err) {
		log.Printf("Chart path %s does not exist", chartPath)
		return nil, err
	}
	if _, err := os.Stat(chartPath + "/values.yaml"); os.IsNotExist(err) {
		log.Printf("values.yaml does not exist in chart path %s", chartPath)
		return nil, err
	}
	// Load values.yaml
	valuesFilePath := chartPath + "/values.yaml"
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

func replaceHub(m map[interface{}]interface{}, newRepo string) {
	for k, v := range m {
		switch val := v.(type) {
		case map[interface{}]interface{}:
			// Recurse into nested maps
			replaceHub(val, newRepo)
		case string:
			if checkRegistryAttr(k) && val != "" {
				// retain the existing image path after the hub. it is expected that the artifactory path matches the predictable structure
				newRepoJoined := path.Join(newRepo, val)
				Logger.Infof("Updating hub from %s to %s", val, newRepoJoined)
				m[k] = newRepoJoined
			}
		}
	}
}

func checkRegistryAttr(key interface{}) bool {
	for _, attr := range RegistryAttrs {
		if key == attr {
			return true
		}
	}
	return false
}
func UpdateValuesHub(values map[interface{}]interface{}, newHub string) {
	fmt.Printf("Before values.yaml loaded from chart %s\n\n\n", values)
	replaceHub(values, newHub)
	fmt.Printf("After values.yaml loaded from chart %s\n\n\n", values)
}

// renderChartLocal renders a chart completely locally using Helm's engine and chartutil
// This does not contact a Kubernetes API server.
func renderChartLocal(chartPath string, values map[string]interface{}) (*release.Release, error) {
	chart, err := loader.Load(chartPath)
	if err != nil {
		Logger.Errorf("chart loader.Load failed: %v", err)
		return nil, err
	}

	// Prepare release options for templating
	relOpts := chartutil.ReleaseOptions{
		Name:      "test",
		Namespace: "default",
	}

	// Create render values (chart values merged with release options and capabilities)
	renderValues, err := chartutil.ToRenderValues(chart, values, relOpts, chartutil.DefaultCapabilities)
	if err != nil {
		Logger.Errorf("chartutil.ToRenderValues failed: %v\n values: %s", err, values)
		return nil, err
	}

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

// WriteUpdatedValuesFile writes the given values as YAML to <chartPath>/updated-values.yaml
// this for initial debugging and testing. Plan is to update the values.yaml in place and create a PR in Jenkins.
func WriteUpdatedValuesFile(chartPath string, values interface{}) error {
	outPath := filepath.Join(chartPath, "updated-values.yaml")
	data, err := yaml.Marshal(values)
	if err != nil {
		return fmt.Errorf("failed to marshal values to YAML: %w", err)
	}
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write updated values file %s: %w", outPath, err)
	}
	Logger.Infof("Wrote updated values to %s", outPath)
	return nil
}

func RenderChartWithValues(chartPath string, values map[interface{}]interface{}, localRepo string) (*release.Release, error) {
	// Update chart repo in values
	replaceHub(values, localRepo)

	// Convert values to map[string]interface{}
	valuesStr := convertMapI2MapS(values).(map[string]interface{})

	// Write updated values to file
	if err := WriteUpdatedValuesFile(chartPath, valuesStr); err != nil {
		Logger.Errorf("failed to write updated values file: %v", err)
		return nil, err
	}

	// Now render the chart with updated values
	rel, err := renderChartLocal(chartPath, valuesStr)
	if err != nil {
		Logger.Errorf("error rendering chart: %s", err)
		return nil, err
	}
	// fmt.Printf("Rendered manifest:\n\n\n%s\n\n", rel.Manifest)
	return rel, nil
}

func ExtractImagesFromManifest(manifest string) ([]string, error) {
	var images []string

	parts := splitDocuments(manifest)
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		var doc interface{}
		if err := yaml.Unmarshal([]byte(p), &doc); err != nil {
			Logger.Warnf("skipping document %d due to yaml unmarshal error: %v", i, err)
			continue
		}

		collectImagesRecursive(doc, &images)
	}

	// Deduplicate
	seen := map[string]struct{}{}
	uniq := make([]string, 0, len(images))
	for _, img := range images {
		if img == "" {
			continue
		}
		if _, ok := seen[img]; !ok {
			seen[img] = struct{}{}
			uniq = append(uniq, img)
		}
	}
	return uniq, nil
}

// splitDocuments splits a YAML manifest into documents using lines that are exactly
// '---' or '...' (allowing leading/trailing whitespace) as boundaries. This is
// more robust than a simple string split since it handles CRLF and variations.
func splitDocuments(manifest string) []string {
	var docs []string
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
	// append final
	if err := s.Err(); err != nil {
		// If scanning fails, fall back to whole manifest as one doc
		return []string{manifest}
	}
	last := strings.TrimSpace(sb.String())
	if last != "" {
		docs = append(docs, last)
	}
	return docs
}

func collectImagesRecursive(node interface{}, images *[]string) {
	switch n := node.(type) {
	case map[interface{}]interface{}:
		for k, v := range n {
			key := fmt.Sprintf("%v", k)
			if key == "containers" || key == "initContainers" {
				if sl, ok := v.([]interface{}); ok {
					for _, item := range sl {
						switch it := item.(type) {
						case map[interface{}]interface{}:
							if img := getImageFromMapI(it); img != "" {
								*images = append(*images, img)
							}
						case map[string]interface{}:
							if img := getImageFromMapS(it); img != "" {
								*images = append(*images, img)
							}
						}
					}
				}
			} else {
				collectImagesRecursive(v, images)
			}
		}
	case map[string]interface{}:
		for k, v := range n {
			if k == "containers" || k == "initContainers" {
				if sl, ok := v.([]interface{}); ok {
					for _, item := range sl {
						switch it := item.(type) {
						case map[interface{}]interface{}:
							if img := getImageFromMapI(it); img != "" {
								*images = append(*images, img)
							}
						case map[string]interface{}:
							if img := getImageFromMapS(it); img != "" {
								*images = append(*images, img)
							}
						}
					}
				}
			} else {
				collectImagesRecursive(v, images)
			}
		}
	case []interface{}:
		for _, e := range n {
			collectImagesRecursive(e, images)
		}
	}
}

func getImageFromMapI(m map[interface{}]interface{}) string {
	for k, v := range m {
		if fmt.Sprintf("%v", k) == "image" {
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

func getImageFromMapS(m map[string]interface{}) string {
	if v, ok := m["image"]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func ProcessChart(chartPath string, localRepo string) error {
	// Load values.yaml from chart and update hub to localRepo
	values, err := LoadValues(chartPath)
	if err != nil {
		Logger.Fatalf("failed to load values: %v", err)
		return err
	}
	rel, err := RenderChartWithValues(chartPath, values, localRepo)
	if err != nil {
		Logger.Fatalf("failed to render chart with values: %v", err)
		return err
	}

	// Parse rendered manifest and extract images from pod specs
	images, err := ExtractImagesFromManifest(rel.Manifest)
	if err != nil {
		Logger.Errorf("failed to extract images from manifest: %v", err)
		return err
	}
	Logger.Infof("rendered images:")
	for _, img := range images {
		Logger.Infof("%s", img)
	}
	// Check if images exist in registry
	imageExistMap, err := CheckImagesExist(context.Background(), images, "", "")
	if err != nil {
		Logger.Errorf("failed to check images existence: %v", err)
	}
	// Log missing images
	failFatal := false
	Logger.Infof("images before check: %s", images)
	for _, img := range images {
		if exists, ok := imageExistMap[img]; ok {
			if !exists {
				Logger.Errorf("Image does not exist in registry: %s", img)
				failFatal = true
			} else {
				Logger.Infof("Image exists in registry: %s", img)
			}
		}
	}
	if failFatal {
		return fmt.Errorf("one or more images do not exist in registry")
	}

	// If no errors write the values file
	valuesStr := convertMapI2MapS(values).(map[string]interface{})

	// Write updated values to file
	if err := WriteUpdatedValuesFile(chartPath, valuesStr); err != nil {
		Logger.Errorf("failed to write updated values file: %v", err)
		return err
	}

	return nil
}

func CheckImagesExist(ctx context.Context, images []string, username, password string) (map[string]bool, error) {
	concurrency := 4
	timeout := 30 * time.Second
	results := make(map[string]bool, len(images))
	var mu sync.Mutex
	var wg sync.WaitGroup

	sem := make(chan struct{}, concurrency)

	// Choose registry authenticator
	var auth regauthn.Authenticator
	if username != "" || password != "" {
		auth = regauthn.FromConfig(regauthn.AuthConfig{Username: username, Password: password})
	} else {
		auth = regauthn.Anonymous
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for _, img := range images {
		wg.Add(1)
		img := img
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				results[img] = false
				mu.Unlock()
				return
			}

			ref, err := regname.ParseReference(img)
			if err != nil {
				Logger.Warnf("failed to parse image reference %s: %v", img, err)
				mu.Lock()
				results[img] = false
				mu.Unlock()
				return
			}

			opts := []regremote.Option{regremote.WithAuth(auth), regremote.WithContext(ctx)}

			// Try a HEAD-like check first
			if _, err := regremote.Head(ref, opts...); err == nil {
				mu.Lock()
				results[img] = true
				mu.Unlock()
				return
			}

			// Fallback to GET manifest
			if _, err := regremote.Get(ref, opts...); err == nil {
				mu.Lock()
				results[img] = true
				mu.Unlock()
				return
			} else {
				// Distinguish common cases by inspecting error text (remote returns wrapped errors).
				Logger.Debugf("remote.Get failed for %s: %v", img, err)
				mu.Lock()
				results[img] = false
				mu.Unlock()
				return
			}
		}()
	}

	wg.Wait()
	return results, nil
}
