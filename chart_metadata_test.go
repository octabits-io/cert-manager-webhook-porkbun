package main

// Artifact Hub parses the artifacthub.io/* annotations in Chart.yaml and
// rejects a chart version outright if any of them is malformed. Nothing in the
// normal toolchain catches that: `helm lint` does not know these annotations,
// and the only signal is a line in the repository's tracking error log on
// artifacthub.io, which nobody is watching.
//
// That is how 2.0.2 and 2.0.3 both shipped an alternative name Artifact Hub
// would not accept, leaving the published listing stranded on 2.0.1 while it
// silently refused every release after it.

import (
	"os"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

const chartPath = "charts/cert-manager-webhook-porkbun/Chart.yaml"

// https://artifacthub.io/docs/topics/repositories/ — the fixed set a chart may
// declare. Anything else fails registration.
var validCategories = map[string]bool{
	"ai-machine-learning":  true,
	"database":             true,
	"integration-delivery": true,
	"monitoring-logging":   true,
	"networking":           true,
	"security":             true,
	"storage":              true,
	"streaming-messaging":  true,
}

var validChangeKinds = map[string]bool{
	"added": true, "changed": true, "deprecated": true,
	"removed": true, "fixed": true, "security": true,
}

type chart struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	AppVersion  string            `json:"appVersion"`
	Annotations map[string]string `json:"annotations"`
}

func loadChart(t *testing.T) chart {
	t.Helper()
	b, err := os.ReadFile(chartPath)
	if err != nil {
		t.Fatalf("reading %s: %v", chartPath, err)
	}
	var c chart
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatalf("parsing %s: %v", chartPath, err)
	}
	return c
}

// The rule that broke 2.0.2 and 2.0.3: an alternative name has to be a
// substring or a superstring of the package name.
func TestArtifactHubAlternativeName(t *testing.T) {
	c := loadChart(t)
	alt, ok := c.Annotations["artifacthub.io/alternativeName"]
	if !ok {
		return // optional
	}
	if !strings.Contains(c.Name, alt) && !strings.Contains(alt, c.Name) {
		t.Errorf("artifacthub.io/alternativeName %q is neither a substring nor a superstring of %q; "+
			"Artifact Hub will reject every version carrying it", alt, c.Name)
	}
}

func TestArtifactHubCategory(t *testing.T) {
	c := loadChart(t)
	cat, ok := c.Annotations["artifacthub.io/category"]
	if !ok {
		return // optional
	}
	if !validCategories[cat] {
		t.Errorf("artifacthub.io/category %q is not one of Artifact Hub's categories", cat)
	}
}

func TestArtifactHubChanges(t *testing.T) {
	c := loadChart(t)
	raw, ok := c.Annotations["artifacthub.io/changes"]
	if !ok {
		return // optional
	}
	var changes []struct {
		Kind        string `json:"kind"`
		Description string `json:"description"`
	}
	if err := yaml.Unmarshal([]byte(raw), &changes); err != nil {
		t.Fatalf("artifacthub.io/changes is not valid YAML: %v", err)
	}
	if len(changes) == 0 {
		t.Error("artifacthub.io/changes is present but empty")
	}
	for i, ch := range changes {
		if !validChangeKinds[ch.Kind] {
			t.Errorf("changes[%d]: kind %q is not one of added/changed/deprecated/removed/fixed/security", i, ch.Kind)
		}
		if strings.TrimSpace(ch.Description) == "" {
			t.Errorf("changes[%d]: description is empty", i)
		}
	}
}

func TestArtifactHubLinks(t *testing.T) {
	c := loadChart(t)
	raw, ok := c.Annotations["artifacthub.io/links"]
	if !ok {
		return // optional
	}
	var links []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := yaml.Unmarshal([]byte(raw), &links); err != nil {
		t.Fatalf("artifacthub.io/links is not valid YAML: %v", err)
	}
	for i, l := range links {
		if l.Name == "" || l.URL == "" {
			t.Errorf("links[%d]: both name and url are required, got %+v", i, l)
		}
		if !strings.HasPrefix(l.URL, "https://") {
			t.Errorf("links[%d]: %q is not an https URL", i, l.URL)
		}
	}
}

// The image tag in artifacthub.io/images is a hand-maintained copy of
// appVersion. A release that bumps one and forgets the other publishes a
// listing pointing at the previous image, which is the kind of drift only a
// reader of the Artifact Hub page would ever notice.
func TestArtifactHubImagesMatchAppVersion(t *testing.T) {
	c := loadChart(t)
	raw, ok := c.Annotations["artifacthub.io/images"]
	if !ok {
		return // optional
	}
	var images []struct {
		Name  string `json:"name"`
		Image string `json:"image"`
	}
	if err := yaml.Unmarshal([]byte(raw), &images); err != nil {
		t.Fatalf("artifacthub.io/images is not valid YAML: %v", err)
	}
	if len(images) == 0 {
		t.Error("artifacthub.io/images is present but empty")
	}
	for i, img := range images {
		if img.Name == "" || img.Image == "" {
			t.Errorf("images[%d]: both name and image are required, got %+v", i, img)
			continue
		}
		_, tag, found := strings.Cut(img.Image, ":")
		if !found {
			t.Errorf("images[%d]: %q has no tag", i, img.Image)
			continue
		}
		if tag != c.AppVersion {
			t.Errorf("images[%d]: tag %q does not match appVersion %q; bump both together",
				i, tag, c.AppVersion)
		}
	}
}

// Artifact Hub renders the README packaged inside the chart, and nothing else:
// the repository README at the root of this repo never reaches the listing.
// Through 2.0.4 there was no chart README, so the Artifact Hub page carried the
// metadata block and no documentation. `helm lint` does not warn about this,
// and `helm package` happily builds a chart without one.
func TestChartReadmeIsPackaged(t *testing.T) {
	const readmePath = "charts/cert-manager-webhook-porkbun/README.md"

	b, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("reading %s: %v; Artifact Hub renders this file as the package "+
			"documentation and shows nothing without it", readmePath, err)
	}
	if len(b) < 500 {
		t.Errorf("%s is %d bytes; that is not documentation", readmePath, len(b))
	}

	// A .helmignore entry would leave the file in git and out of the tarball,
	// which looks identical here and is invisible on Artifact Hub until the
	// release lands.
	ignore, err := os.ReadFile("charts/cert-manager-webhook-porkbun/.helmignore")
	if err != nil {
		t.Fatalf("reading .helmignore: %v", err)
	}
	for _, line := range strings.Split(string(ignore), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(strings.ToUpper(line), "README") {
			t.Errorf(".helmignore excludes the chart README via %q; it would not be packaged", line)
		}
	}
}
