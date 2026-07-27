package solver

import "testing"

func TestResolveTarget(t *testing.T) {
	tests := []struct {
		name          string
		override      string
		zone          string
		fqdn          string
		wantDomain    string
		wantSubdomain string
		wantErr       bool
	}{
		{
			name:          "plain subdomain challenge",
			zone:          "reynt.co.",
			fqdn:          "_acme-challenge.reynt.co.",
			wantDomain:    "reynt.co",
			wantSubdomain: "_acme-challenge",
		},
		{
			name:          "wildcard challenge uses the same record name as the apex",
			zone:          "reynt.co.",
			fqdn:          "_acme-challenge.reynt.co.",
			wantDomain:    "reynt.co",
			wantSubdomain: "_acme-challenge",
		},
		{
			name:          "nested subdomain",
			zone:          "reynt.co.",
			fqdn:          "_acme-challenge.stg.reynt.co.",
			wantDomain:    "reynt.co",
			wantSubdomain: "_acme-challenge.stg",
		},
		{
			name:          "multi-label public suffix",
			zone:          "example.co.uk.",
			fqdn:          "_acme-challenge.example.co.uk.",
			wantDomain:    "example.co.uk",
			wantSubdomain: "_acme-challenge",
		},
		{
			// Porkbun is addressed by registered domain, not by DNS zone. A
			// delegated sub-zone must still resolve to the registered domain,
			// or the API rejects the request as an unknown domain.
			name:          "delegated sub-zone resolves to the registered domain",
			zone:          "sub.example.com.",
			fqdn:          "_acme-challenge.sub.example.com.",
			wantDomain:    "example.com",
			wantSubdomain: "_acme-challenge.sub",
		},
		{
			// strings.Index finds the *first* occurrence, truncating this to
			// "_acme-challenge". Suffix matching is required.
			name:          "repeated label sequence",
			zone:          "example.com.",
			fqdn:          "_acme-challenge.example.com.example.com.",
			wantDomain:    "example.com",
			wantSubdomain: "_acme-challenge.example.com",
		},
		{
			name:          "explicit override wins over derivation",
			override:      "example.com",
			zone:          "sub.example.com.",
			fqdn:          "_acme-challenge.sub.example.com.",
			wantDomain:    "example.com",
			wantSubdomain: "_acme-challenge.sub",
		},
		{
			name:          "override with a trailing dot",
			override:      "example.com.",
			zone:          "example.com.",
			fqdn:          "_acme-challenge.example.com.",
			wantDomain:    "example.com",
			wantSubdomain: "_acme-challenge",
		},
		{
			name:          "challenge at the apex",
			override:      "example.com",
			zone:          "example.com.",
			fqdn:          "example.com.",
			wantDomain:    "example.com",
			wantSubdomain: "",
		},
		{
			name:          "mixed case is normalised",
			zone:          "Reynt.CO.",
			fqdn:          "_ACME-Challenge.Reynt.CO.",
			wantDomain:    "reynt.co",
			wantSubdomain: "_acme-challenge",
		},
		{
			// An unknown TLD is not in the public suffix list; the resolved
			// zone is the only signal left.
			name:          "unknown TLD falls back to the resolved zone",
			zone:          "example.internal.",
			fqdn:          "_acme-challenge.example.internal.",
			wantDomain:    "example.internal",
			wantSubdomain: "_acme-challenge",
		},
		{
			name:     "override that does not contain the challenge name",
			override: "other.com",
			zone:     "example.com.",
			fqdn:     "_acme-challenge.example.com.",
			wantErr:  true,
		},
		{
			name:    "empty FQDN",
			zone:    "example.com.",
			fqdn:    "",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			domain, subdomain, err := resolveTarget(tc.override, tc.zone, tc.fqdn)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got domain=%q subdomain=%q", domain, subdomain)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if domain != tc.wantDomain {
				t.Errorf("domain = %q, want %q", domain, tc.wantDomain)
			}
			if subdomain != tc.wantSubdomain {
				t.Errorf("subdomain = %q, want %q", subdomain, tc.wantSubdomain)
			}
		})
	}
}

// The subdomain must recombine with the domain to reproduce the original
// challenge name; if it does not, the TXT record lands at the wrong name and
// the challenge silently never validates.
func TestSubdomainRoundTrips(t *testing.T) {
	fqdns := []string{
		"_acme-challenge.reynt.co.",
		"_acme-challenge.stg.reynt.co.",
		"_acme-challenge.a.b.c.example.com.",
		"_acme-challenge.example.co.uk.",
	}

	for _, fqdn := range fqdns {
		t.Run(fqdn, func(t *testing.T) {
			domain, subdomain, err := resolveTarget("", "", fqdn)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got, want := displayName(subdomain, domain), normalizeName(fqdn); got != want {
				t.Errorf("recombined name = %q, want %q", got, want)
			}
		})
	}
}

func TestNormalizeName(t *testing.T) {
	tests := map[string]string{
		"example.com.":  "example.com",
		"example.com":   "example.com",
		" example.com ": "example.com",
		"":              "",
		".":             "",
	}

	for in, want := range tests {
		if got := normalizeName(in); got != want {
			t.Errorf("normalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}
