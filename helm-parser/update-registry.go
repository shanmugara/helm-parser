package helm_parser

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	regauthn "github.com/google/go-containerregistry/pkg/authn"
	regname "github.com/google/go-containerregistry/pkg/name"
	regremote "github.com/google/go-containerregistry/pkg/v1/remote"

	"gopkg.in/yaml.v2"
)

func UpdateRegistryName(chartPath string, values map[interface{}]interface{}, localRepo string) error {
	// Update registry paths in values.yaml using text-based manipulation
	// This preserves comments, order, and formatting
	if err := UpdateRegistryInValuesFile(chartPath, localRepo); err != nil {
		Logger.Errorf("failed to update registry in values file: %v", err)
		return err
	}
	return nil

	// // Read the updated values back for rendering
	// valuesPath := filepath.Join(chartPath, "values.yaml")
	// updatedValues, err := os.ReadFile(valuesPath)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to read updated values: %v", err)
	// }

	// // Parse updated values - unmarshal into map[interface{}]interface{} first
	// var valuesMapI map[interface{}]interface{}
	// if err := yaml.Unmarshal(updatedValues, &valuesMapI); err != nil {
	// 	return nil, fmt.Errorf("failed to unmarshal updated values: %v", err)
	// }

	// // Convert to map[string]interface{} recursively to avoid JSON schema validation errors
	// valuesMap := convertMapI2MapS(valuesMapI).(map[string]interface{})

	// // Now render the chart with updated values
	// rel, err := renderChartLocal(chartPath, valuesMap)
	// if err != nil {
	// 	Logger.Errorf("error rendering chart: %s", err)
	// 	return nil, err
	// }

	// return rel, nil
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
				if img == "auto" {
					Logger.Warnf("special case: 'auto' image reference encountered, treating as existing without validation")
					// Special case: "auto" is not a valid image, treat as non-existent without logging
					results[img] = true

				} else {
					results[img] = false
				}
				mu.Unlock()
				return
			}
		}()
	}

	wg.Wait()
	return results, nil
}
