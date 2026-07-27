//go:build conformance

// The cert-manager DNS01 conformance suite drives a real ACME challenge
// against a real Porkbun domain: it creates a TXT record, waits for it to
// propagate to the authoritative nameservers, and deletes it again.
//
// It therefore needs live credentials and a domain you control, and it is
// gated behind a build tag so it never runs by accident in CI. See
// testdata/README.md for the setup, then:
//
//	make test-conformance TEST_ZONE_NAME=example.com.
package main

import (
	"os"
	"testing"
	"time"

	acmetest "github.com/cert-manager/cert-manager/test/acme"

	"github.com/octabits-io/cert-manager-webhook-porkbun/internal/solver"
)

func TestConformance(t *testing.T) {
	zone := os.Getenv("TEST_ZONE_NAME")
	if zone == "" {
		t.Skip("TEST_ZONE_NAME is not set; see testdata/README.md")
	}

	fixture := acmetest.NewFixture(solver.New(),
		acmetest.SetResolvedZone(zone),
		acmetest.SetAllowAmbientCredentials(false),
		acmetest.SetManifestPath("testdata/porkbun"),
		acmetest.SetStrict(true),
		// Porkbun's nameservers are not instant. The default limit is tight
		// enough to produce spurious failures.
		acmetest.SetPropagationLimit(5*time.Minute),
		acmetest.SetPollInterval(10*time.Second),
	)

	fixture.RunConformance(t)
}
