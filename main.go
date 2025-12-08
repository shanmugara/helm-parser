package main

import (
	"fmt"
	"os"

	helm_parser "helm-parser/helm-parser"

	"github.com/spf13/cobra"
)

const (
	CHART_DIR     = "/Users/speriya/istio-1.26.2/manifests/charts/gateway"
	TEMPLATES_DIR = "templates/"
	LOCAL_REPO    = "registry.omegaworld.net/ext"
)

var (
	chartDir       string
	templatesDir   string
	localRepo      string
	customYaml     string
	criticalDs     bool
	controlPlane   bool
	systemCritical string
	dryRun         bool
)

var rootCmd = &cobra.Command{
	Use:   "helm-parser",
	Short: "Helm chart parser and modifier",
	Long: `A tool to parse Helm charts, inject custom blocks, and update container registries.
It can inject pod-level and container-level configurations into Helm templates or values.yaml files.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return helm_parser.ProcessChart(chartDir, localRepo, customYaml, criticalDs, controlPlane, systemCritical, dryRun)
	},
}

func init() {
	rootCmd.Flags().StringVar(&localRepo, "local-repo", LOCAL_REPO, "Local repository prefix for images")
	rootCmd.Flags().StringVar(&chartDir, "chart-dir", CHART_DIR, "Path to the Helm chart directory")
	rootCmd.Flags().StringVar(&templatesDir, "templates-dir", TEMPLATES_DIR, "Path to the templates directory within the chart")
	rootCmd.Flags().StringVar(&customYaml, "custom-yaml", "inject-blocks.yaml", "Path to a custom YAML file with injection blocks")
	rootCmd.Flags().BoolVar(&criticalDs, "critical-ds", false, "Enable critical DaemonSet processing (adds criticalDsPods blocks)")
	rootCmd.Flags().BoolVar(&controlPlane, "control-plane", false, "Enable control plane processing (adds controlPlanePods blocks)")
	rootCmd.Flags().StringVar(&systemCritical, "system-critical", "", "Specify system critical component")
	rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Enable dry run mode (show changes without modifying files)")

	// Mark required flags if needed
	// rootCmd.MarkFlagRequired("chart-dir")
	rootCmd.RegisterFlagCompletionFunc("system-critical", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"node", "system", ""}, cobra.ShellCompDirectiveNoFileComp
	})
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
