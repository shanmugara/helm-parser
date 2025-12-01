package main

import (
	"flag"
	helm_parser "helm-parser/helm-parser"
	"log"
)

const (
	CHART_DIR     = "/Users/speriya/istio-1.26.2/manifests/charts/gateway"
	TEMPLATES_DIR = "templates/"
	LOCAL_REPO    = "registry.omegaworld.net/ext/"
)

func main() {
	var chartDir string
	var templatesDir string
	var localRepo string
	flag.StringVar(&localRepo, "local-repo", LOCAL_REPO, "Local repository prefix for images")
	flag.StringVar(&chartDir, "chart-dir", CHART_DIR, "Path to the Helm chart directory")
	flag.StringVar(&templatesDir, "templates-dir", TEMPLATES_DIR, "Path to the templates directory within the chart")
	flag.Parse()

	if err := helm_parser.ProcessChart(chartDir, localRepo); err != nil {
		log.Fatalf("ProcessChart failed: %v", err)
	}
}
