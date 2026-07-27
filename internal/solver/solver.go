// Package solver implements the cert-manager ACME DNS01 webhook solver for
// Porkbun-hosted domains.
package solver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	acme "github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	extapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/octabits-io/cert-manager-webhook-porkbun/internal/porkbun"
)

const (
	// SolverName is the value to use for `solverName` in an Issuer's dns01
	// webhook stanza.
	SolverName = "porkbun"

	// recordType is the only record type this solver manages.
	recordType = "TXT"

	// minTTL is the lowest TTL Porkbun accepts. Their published minimum has
	// moved between 300 and 600 seconds; 600 is valid under either, and the
	// TTL of an ACME challenge record has no practical effect because
	// cert-manager polls authoritative nameservers directly rather than
	// waiting for a cache to expire. Requests below this are clamped up
	// rather than rejected, because a too-low TTL is otherwise an opaque
	// "invalid TTL" failure at issuance time.
	minTTL = 600

	// defaultTimeout bounds a single Present or CleanUp call, inclusive of
	// retries. cert-manager will call again on the next reconcile.
	defaultTimeout = 2 * time.Minute
)

// dnsAPI is the subset of the Porkbun client the solver uses. It exists so
// tests can substitute a stub without reaching the network.
type dnsAPI interface {
	RetrieveRecordsByNameType(ctx context.Context, domain, recordType, subdomain string) ([]porkbun.Record, error)
	CreateRecord(ctx context.Context, domain string, record porkbun.Record) (string, error)
	DeleteRecord(ctx context.Context, domain, id string) error
}

// Config is the per-issuer configuration decoded from the `config` block of an
// Issuer's dns01 webhook solver.
//
// The apiKey/secretApiKey field names are retained from the upstream project
// this is forked from so existing Issuers keep working unchanged. The
// apiKeySecretRef/secretKeySecretRef aliases match cert-manager's own naming
// convention and are accepted as equivalents.
type Config struct {
	APIKey       *corev1.SecretKeySelector `json:"apiKey,omitempty"`
	SecretAPIKey *corev1.SecretKeySelector `json:"secretApiKey,omitempty"`

	APIKeySecretRef    *corev1.SecretKeySelector `json:"apiKeySecretRef,omitempty"`
	SecretKeySecretRef *corev1.SecretKeySelector `json:"secretKeySecretRef,omitempty"`

	// Domain optionally pins the registered domain to operate on. Porkbun's
	// API is addressed by registered domain, not by DNS zone, so this is an
	// escape hatch for setups where the two differ and the automatic
	// derivation gets it wrong.
	Domain string `json:"domain,omitempty"`

	// TTL for the challenge TXT record, in seconds. Values below the Porkbun
	// minimum are clamped up.
	TTL int `json:"ttl,omitempty"`
}

// apiKeyRef returns the effective API key selector, preferring the canonical
// field over the alias.
func (c *Config) apiKeyRef() *corev1.SecretKeySelector {
	if c.APIKey != nil {
		return c.APIKey
	}
	return c.APIKeySecretRef
}

// secretKeyRef returns the effective secret API key selector.
func (c *Config) secretKeyRef() *corev1.SecretKeySelector {
	if c.SecretAPIKey != nil {
		return c.SecretAPIKey
	}
	return c.SecretKeySecretRef
}

func (c *Config) validate() error {
	var errs []error

	if ref := c.apiKeyRef(); ref == nil || ref.Name == "" {
		errs = append(errs, errors.New("apiKey.name (or apiKeySecretRef.name) must be set"))
	} else if ref.Key == "" {
		errs = append(errs, errors.New("apiKey.key must be set"))
	}

	if ref := c.secretKeyRef(); ref == nil || ref.Name == "" {
		errs = append(errs, errors.New("secretApiKey.name (or secretKeySecretRef.name) must be set"))
	} else if ref.Key == "" {
		errs = append(errs, errors.New("secretApiKey.key must be set"))
	}

	if c.TTL < 0 {
		errs = append(errs, fmt.Errorf("ttl must not be negative, got %d", c.TTL))
	}

	return errors.Join(errs...)
}

func (c *Config) ttl() string {
	if c.TTL < minTTL {
		return strconv.Itoa(minTTL)
	}
	return strconv.Itoa(c.TTL)
}

// Solver implements webhook.Solver for Porkbun.
type Solver struct {
	kube kubernetes.Interface

	// newAPI builds a Porkbun client from credentials. Overridden in tests.
	newAPI func(secretAPIKey, apiKey string) dnsAPI

	// timeout bounds a single Present/CleanUp call.
	timeout time.Duration

	// domainLocks serialises operations per registered domain.
	//
	// A certificate covering both example.com and *.example.com produces two
	// concurrent challenges that read and write the same _acme-challenge TXT
	// record set. Serialising them removes a read-modify-write race on the
	// idempotency check and roughly halves the request rate against an API
	// that rate limits aggressively.
	domainLocks sync.Map // map[string]*sync.Mutex
}

// New returns a Solver ready to be registered with the webhook server.
func New() *Solver {
	return &Solver{
		newAPI: func(secretAPIKey, apiKey string) dnsAPI {
			return porkbun.New(secretAPIKey, apiKey)
		},
		timeout: defaultTimeout,
	}
}

// Name is the solver name referenced by an Issuer's dns01 webhook config.
func (s *Solver) Name() string { return SolverName }

// Initialize is called as a post-start hook when the webhook apiserver starts.
func (s *Solver) Initialize(kubeClientConfig *rest.Config, _ <-chan struct{}) error {
	cl, err := kubernetes.NewForConfig(kubeClientConfig)
	if err != nil {
		return fmt.Errorf("building kubernetes client: %w", err)
	}
	s.kube = cl

	klog.InfoS("porkbun solver initialised", "solver", SolverName)
	return nil
}

// Present creates the ACME challenge TXT record. It is idempotent: if a record
// with the same value already exists it returns without calling the API again.
func (s *Solver) Present(ch *acme.ChallengeRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	cfg, api, domain, subdomain, err := s.setup(ctx, ch)
	if err != nil {
		return err
	}

	unlock := s.lockDomain(domain)
	defer unlock()

	klog.InfoS("presenting challenge",
		"fqdn", ch.ResolvedFQDN, "zone", ch.ResolvedZone,
		"domain", domain, "subdomain", subdomain)

	existing, err := api.RetrieveRecordsByNameType(ctx, domain, recordType, subdomain)
	if err != nil {
		return fmt.Errorf("retrieving %s records for %q: %w", recordType, displayName(subdomain, domain), err)
	}

	for _, rec := range existing {
		if rec.Content == ch.Key {
			klog.InfoS("challenge record already present, nothing to do",
				"record", displayName(subdomain, domain), "id", rec.ID)
			return nil
		}
	}

	id, err := api.CreateRecord(ctx, domain, porkbun.Record{
		Name:    subdomain,
		Type:    recordType,
		Content: ch.Key,
		TTL:     cfg.ttl(),
	})
	if err != nil {
		return fmt.Errorf("creating %s record %q: %w", recordType, displayName(subdomain, domain), err)
	}

	klog.InfoS("created challenge record", "record", displayName(subdomain, domain), "id", id)
	return nil
}

// CleanUp removes the challenge TXT records matching this challenge's key,
// leaving records belonging to other concurrent challenges intact.
func (s *Solver) CleanUp(ch *acme.ChallengeRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	_, api, domain, subdomain, err := s.setup(ctx, ch)
	if err != nil {
		return err
	}

	unlock := s.lockDomain(domain)
	defer unlock()

	klog.InfoS("cleaning up challenge",
		"fqdn", ch.ResolvedFQDN, "domain", domain, "subdomain", subdomain)

	existing, err := api.RetrieveRecordsByNameType(ctx, domain, recordType, subdomain)
	if err != nil {
		return fmt.Errorf("retrieving %s records for %q: %w", recordType, displayName(subdomain, domain), err)
	}

	var errs []error
	deleted := 0

	for _, rec := range existing {
		if rec.Content != ch.Key {
			continue
		}

		switch err := api.DeleteRecord(ctx, domain, rec.ID); {
		case err == nil:
			deleted++
			klog.InfoS("deleted challenge record", "record", displayName(subdomain, domain), "id", rec.ID)
		case errors.Is(err, porkbun.ErrRecordNotFound):
			// Already gone. That is the desired end state, so not an error.
			klog.InfoS("challenge record already removed", "record", displayName(subdomain, domain), "id", rec.ID)
		default:
			// Keep going: one undeletable record must not strand the others.
			errs = append(errs, fmt.Errorf("deleting record %s: %w", rec.ID, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleaning up %q: %w", displayName(subdomain, domain), errors.Join(errs...))
	}

	if deleted == 0 {
		klog.InfoS("no matching challenge record to delete", "record", displayName(subdomain, domain))
	}

	return nil
}

// setup decodes and validates the config, resolves credentials, and works out
// which registered domain and subdomain to operate on.
func (s *Solver) setup(ctx context.Context, ch *acme.ChallengeRequest) (*Config, dnsAPI, string, string, error) {
	cfg, err := loadConfig(ch.Config)
	if err != nil {
		return nil, nil, "", "", err
	}

	// Validate unconditionally.
	//
	// The upstream project skipped validation whenever
	// ch.AllowAmbientCredentials was set, which cert-manager sets by default
	// for ClusterIssuers. Porkbun has no notion of ambient credentials, so
	// that only ever converted a clear configuration error into an opaque
	// authentication failure against the API.
	if err := cfg.validate(); err != nil {
		return nil, nil, "", "", fmt.Errorf("invalid porkbun solver config: %w", err)
	}

	if s.kube == nil {
		return nil, nil, "", "", errors.New("solver is not initialised: no kubernetes client")
	}

	apiKey, err := s.secretValue(ctx, ch.ResourceNamespace, cfg.apiKeyRef())
	if err != nil {
		return nil, nil, "", "", err
	}

	secretAPIKey, err := s.secretValue(ctx, ch.ResourceNamespace, cfg.secretKeyRef())
	if err != nil {
		return nil, nil, "", "", err
	}

	domain, subdomain, err := resolveTarget(cfg.Domain, ch.ResolvedZone, ch.ResolvedFQDN)
	if err != nil {
		return nil, nil, "", "", err
	}

	return cfg, s.newAPI(secretAPIKey, apiKey), domain, subdomain, nil
}

// secretValue reads a single key out of a Secret.
func (s *Solver) secretValue(ctx context.Context, namespace string, ref *corev1.SecretKeySelector) (string, error) {
	secret, err := s.kube.CoreV1().Secrets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("reading secret %s/%s: %w", namespace, ref.Name, err)
	}

	value, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("secret %s/%s has no key %q", namespace, ref.Name, ref.Key)
	}

	if len(value) == 0 {
		return "", fmt.Errorf("secret %s/%s key %q is empty", namespace, ref.Name, ref.Key)
	}

	return string(value), nil
}

// lockDomain acquires the per-domain lock and returns its release function.
func (s *Solver) lockDomain(domain string) func() {
	v, _ := s.domainLocks.LoadOrStore(domain, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// loadConfig decodes the solver config block.
func loadConfig(raw *extapi.JSON) (*Config, error) {
	cfg := &Config{}
	if raw == nil {
		return cfg, nil
	}

	if err := json.Unmarshal(raw.Raw, cfg); err != nil {
		return nil, fmt.Errorf("decoding porkbun solver config: %w", err)
	}

	return cfg, nil
}

func displayName(subdomain, domain string) string {
	if subdomain == "" {
		return domain
	}
	return subdomain + "." + domain
}
