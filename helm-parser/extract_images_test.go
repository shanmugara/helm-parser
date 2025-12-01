package helm_parser

import (
	"testing"
)

func TestExtractImagesFromManifest_SkipsBadDocs(t *testing.T) {
	manifest := `apiVersion: v1
kind: Pod
metadata:
  name: good-pod
spec:
  containers:
  - name: app
    image: repo/app:1.2.3
---
this is not: valid: yaml: [
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: deploy
spec:
  template:
    spec:
      containers:
      - name: dapp
        image: repo/deploy:4.5.6
`

	images, err := ExtractImagesFromManifest(manifest)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("expected 2 images, got %d: %v", len(images), images)
	}
	// Simple containment checks
	found1 := false
	found2 := false
	for _, img := range images {
		if img == "repo/app:1.2.3" {
			found1 = true
		}
		if img == "repo/deploy:4.5.6" {
			found2 = true
		}
	}
	if !found1 || !found2 {
		t.Fatalf("did not find expected images in result: %v", images)
	}
}
