package helm_parser

import (
	"testing"
)

func Test_loadInjectorBlocks(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		customYaml string
		want       InjectorBlocks
		wantErr    bool
	}{
		{
			name:       "istio-test",
			customYaml: "inject-blocks.yaml",
			want:       InjectorBlocks{},
			wantErr:    false,
		},
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := loadInjectorBlocks(tt.customYaml)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("loadInjectorBlocks() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("loadInjectorBlocks() succeeded unexpectedly")
			}
			// TODO: update the condition below to compare got with tt.want.
			if true {
				// t.Errorf("loadInjectorBlocks() = %v, want %v", got, tt.want)
				t.Logf("\n===================================\n")
				t.Logf("\nloadInjectorBlocks() = \n%v\n", got)
			}
		})
	}
}
