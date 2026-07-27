// Command cert-manager-webhook-porkbun runs an ACME DNS01 solver webhook for
// domains hosted at Porkbun.
//
// The entry-point structure is derived from cert-manager/webhook-example
// (Apache-2.0, Copyright The cert-manager Authors), by way of
// Talinx/cert-manager-webhook-porkbun. Modified: the GROUP_NAME check reports
// a usable message and a non-zero exit status instead of panicking. See NOTICE.
package main

import (
	"fmt"
	"os"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook/cmd"

	"github.com/octabits-io/cert-manager-webhook-porkbun/internal/solver"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	groupName := os.Getenv("GROUP_NAME")
	if groupName == "" {
		fmt.Fprintln(os.Stderr, "GROUP_NAME must be set to the API group this webhook serves, "+
			"matching the `groupName` in your Issuer's dns01 webhook config")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "cert-manager-webhook-porkbun %s serving group %q\n", version, groupName)

	// RunWebhookServer installs its own signal handling and calls os.Exit.
	cmd.RunWebhookServer(groupName, solver.New())
}
