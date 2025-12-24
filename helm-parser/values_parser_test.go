package helm_parser_test

import (
	helm_parser "helm-parser/helm-parser"
	"os"
	"testing"
)

func TestDetectValueReferences(t *testing.T) {
	content, err := os.ReadFile("../test-files/istio-deployment.yaml")
	if err != nil {
		t.Fatalf("Failed to read test file: %v", err)
	}

	templateContent := string(content)
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		templateContent string
		want            []helm_parser.ValueReference
	}{
		{
			name:            "istio-deployment",
			templateContent: templateContent,
			want:            []helm_parser.ValueReference{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := helm_parser.DetectValueReferences(tt.templateContent)
			// TODO: update the condition below to compare got with tt.want.
			if true {
				// t.Errorf("DetectValueReferences() = %v, want %v", got, tt.want)
				t.Logf("\n===================================\n")
				t.Logf("\nDetectValueReferences()\n")
				for k, v := range got {
					t.Logf("  %d: %+v\n", k, v)
				}
			}
		})
	}
}
