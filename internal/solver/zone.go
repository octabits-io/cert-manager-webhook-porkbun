package solver

import (
	"fmt"
	"strings"

	"golang.org/x/net/publicsuffix"
	"k8s.io/klog/v2"
)

// resolveTarget works out which Porkbun-registered domain to address and which
// subdomain within it holds the challenge record.
//
// This is subtler than it looks. The Porkbun API is addressed by *registered
// domain*, not by DNS zone, and cert-manager hands us a zone derived from an
// SOA lookup. Those coincide for a plain setup, but diverge when the challenge
// name sits in a delegated sub-zone (`sub.example.com` with its own NS
// records), in which case passing the zone straight through makes Porkbun
// reject the request as an unknown domain.
//
// The registered domain is therefore derived from the FQDN using the public
// suffix list, which yields the same answer as the zone in the common case and
// the correct one otherwise. An explicit override wins over both.
//
// The returned subdomain is the label sequence below the domain, empty for the
// apex.
func resolveTarget(override, resolvedZone, resolvedFQDN string) (domain, subdomain string, err error) {
	fqdn := normalizeName(resolvedFQDN)
	if fqdn == "" {
		return "", "", fmt.Errorf("challenge has an empty resolved FQDN")
	}

	domain, err = registeredDomain(override, resolvedZone, fqdn)
	if err != nil {
		return "", "", err
	}

	subdomain, err = splitSubdomain(fqdn, domain)
	if err != nil {
		return "", "", err
	}

	return domain, subdomain, nil
}

func registeredDomain(override, resolvedZone, fqdn string) (string, error) {
	if d := normalizeName(override); d != "" {
		return d, nil
	}

	zone := normalizeName(resolvedZone)

	etldPlusOne, err := publicsuffix.EffectiveTLDPlusOne(fqdn)
	if err != nil {
		// The public suffix list did not recognise the TLD. This happens for
		// private/internal TLDs and for entries added since this binary was
		// built. Fall back to the zone cert-manager resolved.
		if zone == "" {
			return "", fmt.Errorf("cannot determine the registered domain for %q: %w "+
				"(set `domain` in the solver config to specify it explicitly)", fqdn, err)
		}
		klog.V(2).InfoS("public suffix lookup failed, falling back to the resolved zone",
			"fqdn", fqdn, "zone", zone, "err", err)
		return zone, nil
	}

	if zone != "" && !strings.EqualFold(zone, etldPlusOne) {
		// Worth surfacing: it means the challenge sits in a delegated
		// sub-zone, and we are deliberately not using the zone cert-manager
		// gave us.
		klog.InfoS("resolved zone differs from the registered domain; using the registered domain for the Porkbun API",
			"zone", zone, "registeredDomain", etldPlusOne,
			"hint", "set `domain` in the solver config to override")
	}

	return etldPlusOne, nil
}

// splitSubdomain returns the portion of fqdn below domain.
//
// The comparison is a case-insensitive label-boundary suffix match. The
// upstream implementation used strings.Index, which finds the *first*
// occurrence of the domain anywhere in the name and so truncates incorrectly
// whenever a label sequence repeats — for example
// `_acme-challenge.example.com.example.com`.
func splitSubdomain(fqdn, domain string) (string, error) {
	f := strings.ToLower(strings.TrimSuffix(fqdn, "."))
	d := strings.ToLower(strings.TrimSuffix(domain, "."))

	if d == "" {
		return "", fmt.Errorf("empty registered domain for %q", fqdn)
	}

	if f == d {
		// Challenge at the zone apex.
		return "", nil
	}

	if !strings.HasSuffix(f, "."+d) {
		return "", fmt.Errorf("challenge name %q is not within domain %q", fqdn, domain)
	}

	return f[:len(f)-len(d)-1], nil
}

// normalizeName puts a DNS name into the canonical form used throughout this
// package: no surrounding whitespace, no trailing dot, lower case.
//
// Lowercasing matters because DNS names compare case-insensitively while Go
// string comparison and the public suffix list do not. cert-manager may hand
// us a name in any case, and Porkbun stores record names verbatim.
func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
}
