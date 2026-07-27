package solver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	acme "github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	extapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/octabits-io/cert-manager-webhook-porkbun/internal/porkbun"
)

// fakeAPI records the calls made against it.
type fakeAPI struct {
	mu sync.Mutex

	records []porkbun.Record

	retrieveCalls []retrieveCall
	created       []createCall
	deleted       []string

	retrieveErr error
	createErr   error
	deleteErr   error

	// onCall is invoked at the start of every method, for concurrency tests.
	onCall func()
}

type retrieveCall struct{ domain, recordType, subdomain string }
type createCall struct {
	domain string
	record porkbun.Record
}

func (f *fakeAPI) RetrieveRecordsByNameType(_ context.Context, domain, recordType, subdomain string) ([]porkbun.Record, error) {
	if f.onCall != nil {
		f.onCall()
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	f.retrieveCalls = append(f.retrieveCalls, retrieveCall{domain, recordType, subdomain})
	if f.retrieveErr != nil {
		return nil, f.retrieveErr
	}
	return append([]porkbun.Record(nil), f.records...), nil
}

func (f *fakeAPI) CreateRecord(_ context.Context, domain string, r porkbun.Record) (string, error) {
	if f.onCall != nil {
		f.onCall()
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	f.created = append(f.created, createCall{domain, r})
	if f.createErr != nil {
		return "", f.createErr
	}
	return "1", nil
}

func (f *fakeAPI) DeleteRecord(_ context.Context, _ string, id string) error {
	if f.onCall != nil {
		f.onCall()
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	f.deleted = append(f.deleted, id)
	return f.deleteErr
}

func testSolver(t *testing.T, api dnsAPI, secrets ...*corev1.Secret) *Solver {
	t.Helper()

	objs := make([]runtime.Object, 0, len(secrets))
	for _, s := range secrets {
		objs = append(objs, s)
	}

	s := New()
	s.kube = fake.NewSimpleClientset(objs...)
	s.newAPI = func(string, string) dnsAPI { return api }
	return s
}

func credsSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "porkbun-api-credentials", Namespace: "cert-manager"},
		Data: map[string][]byte{
			"api-key":    []byte("pk1_abc"),
			"secret-key": []byte("sk1_xyz"),
		},
	}
}

func challenge(t *testing.T, cfg map[string]any, fqdn, zone, key string) *acme.ChallengeRequest {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshalling config: %v", err)
	}
	return &acme.ChallengeRequest{
		ResolvedFQDN:      fqdn,
		ResolvedZone:      zone,
		Key:               key,
		ResourceNamespace: "cert-manager",
		Config:            &extapi.JSON{Raw: raw},
	}
}

func defaultConfig() map[string]any {
	return map[string]any{
		"apiKey":       map[string]any{"name": "porkbun-api-credentials", "key": "api-key"},
		"secretApiKey": map[string]any{"name": "porkbun-api-credentials", "key": "secret-key"},
	}
}

func TestPresentCreatesRecord(t *testing.T) {
	api := &fakeAPI{}
	s := testSolver(t, api, credsSecret())

	ch := challenge(t, defaultConfig(), "_acme-challenge.reynt.co.", "reynt.co.", "token-1")
	if err := s.Present(ch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(api.created) != 1 {
		t.Fatalf("created %d records, want 1", len(api.created))
	}
	got := api.created[0]
	if got.domain != "reynt.co" {
		t.Errorf("domain = %q, want reynt.co", got.domain)
	}
	if got.record.Name != "_acme-challenge" {
		t.Errorf("record name = %q, want _acme-challenge", got.record.Name)
	}
	if got.record.Type != "TXT" {
		t.Errorf("record type = %q, want TXT", got.record.Type)
	}
	if got.record.Content != "token-1" {
		t.Errorf("record content = %q, want token-1", got.record.Content)
	}
}

// Porkbun rejects a TTL below its minimum, which upstream hit by hard-coding
// "60". Anything lower must be clamped, not passed through.
func TestPresentClampsTTL(t *testing.T) {
	tests := []struct {
		name string
		ttl  any
		want string
	}{
		{"unset", nil, "600"},
		{"below minimum", 60, "600"},
		{"at minimum", 600, "600"},
		{"above minimum", 3600, "3600"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeAPI{}
			s := testSolver(t, api, credsSecret())

			cfg := defaultConfig()
			if tc.ttl != nil {
				cfg["ttl"] = tc.ttl
			}

			if err := s.Present(challenge(t, cfg, "_acme-challenge.reynt.co.", "reynt.co.", "tok")); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := api.created[0].record.TTL; got != tc.want {
				t.Errorf("ttl = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPresentIsIdempotent(t *testing.T) {
	api := &fakeAPI{records: []porkbun.Record{
		{ID: "10", Name: "_acme-challenge.reynt.co", Type: "TXT", Content: "token-1"},
	}}
	s := testSolver(t, api, credsSecret())

	if err := s.Present(challenge(t, defaultConfig(), "_acme-challenge.reynt.co.", "reynt.co.", "token-1")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(api.created) != 0 {
		t.Errorf("created %d records, want 0 when the record already exists", len(api.created))
	}
}

// A certificate for both example.com and *.example.com produces two challenges
// at the same name with different keys. Presenting one must not suppress the
// other.
func TestPresentAddsSecondRecordForWildcardAndApex(t *testing.T) {
	api := &fakeAPI{records: []porkbun.Record{
		{ID: "10", Name: "_acme-challenge.reynt.co", Type: "TXT", Content: "token-apex"},
	}}
	s := testSolver(t, api, credsSecret())

	if err := s.Present(challenge(t, defaultConfig(), "_acme-challenge.reynt.co.", "reynt.co.", "token-wildcard")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(api.created) != 1 {
		t.Fatalf("created %d records, want 1", len(api.created))
	}
	if api.created[0].record.Content != "token-wildcard" {
		t.Errorf("content = %q, want token-wildcard", api.created[0].record.Content)
	}
}

func TestCleanUpDeletesOnlyMatchingKey(t *testing.T) {
	api := &fakeAPI{records: []porkbun.Record{
		{ID: "10", Type: "TXT", Content: "token-apex"},
		{ID: "11", Type: "TXT", Content: "token-wildcard"},
		{ID: "12", Type: "TXT", Content: "token-wildcard"}, // duplicate
	}}
	s := testSolver(t, api, credsSecret())

	if err := s.CleanUp(challenge(t, defaultConfig(), "_acme-challenge.reynt.co.", "reynt.co.", "token-wildcard")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"11", "12"}
	if len(api.deleted) != len(want) {
		t.Fatalf("deleted %v, want %v", api.deleted, want)
	}
	for i, id := range want {
		if api.deleted[i] != id {
			t.Errorf("deleted[%d] = %q, want %q", i, api.deleted[i], id)
		}
	}
}

func TestCleanUpToleratesAlreadyDeletedRecord(t *testing.T) {
	api := &fakeAPI{
		records:   []porkbun.Record{{ID: "10", Type: "TXT", Content: "tok"}},
		deleteErr: porkbun.ErrRecordNotFound,
	}
	s := testSolver(t, api, credsSecret())

	if err := s.CleanUp(challenge(t, defaultConfig(), "_acme-challenge.reynt.co.", "reynt.co.", "tok")); err != nil {
		t.Errorf("CleanUp should treat an already-deleted record as success, got: %v", err)
	}
}

func TestCleanUpWithNoMatchingRecordSucceeds(t *testing.T) {
	api := &fakeAPI{}
	s := testSolver(t, api, credsSecret())

	if err := s.CleanUp(challenge(t, defaultConfig(), "_acme-challenge.reynt.co.", "reynt.co.", "tok")); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(api.deleted) != 0 {
		t.Errorf("deleted %v, want nothing", api.deleted)
	}
}

// Upstream skipped config validation whenever AllowAmbientCredentials was set,
// which cert-manager sets by default for ClusterIssuers. Porkbun has no
// ambient credentials, so validation must always run.
func TestValidationRunsEvenWithAmbientCredentials(t *testing.T) {
	api := &fakeAPI{}
	s := testSolver(t, api, credsSecret())

	ch := challenge(t, map[string]any{}, "_acme-challenge.reynt.co.", "reynt.co.", "tok")
	ch.AllowAmbientCredentials = true

	err := s.Present(ch)
	if err == nil {
		t.Fatal("expected a validation error for an empty config")
	}
	if !strings.Contains(err.Error(), "apiKey") {
		t.Errorf("error %q should name the missing field", err)
	}
	if len(api.created) != 0 {
		t.Error("no API call should be made with an invalid config")
	}
}

func TestConfigAcceptsCertManagerStyleAliases(t *testing.T) {
	api := &fakeAPI{}
	s := testSolver(t, api, credsSecret())

	cfg := map[string]any{
		"apiKeySecretRef":    map[string]any{"name": "porkbun-api-credentials", "key": "api-key"},
		"secretKeySecretRef": map[string]any{"name": "porkbun-api-credentials", "key": "secret-key"},
	}
	if err := s.Present(challenge(t, cfg, "_acme-challenge.reynt.co.", "reynt.co.", "tok")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(api.created) != 1 {
		t.Errorf("created %d records, want 1", len(api.created))
	}
}

func TestMissingSecretIsReportedClearly(t *testing.T) {
	s := testSolver(t, &fakeAPI{}) // no secrets

	err := s.Present(challenge(t, defaultConfig(), "_acme-challenge.reynt.co.", "reynt.co.", "tok"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "cert-manager/porkbun-api-credentials") {
		t.Errorf("error %q should name the namespace and secret", err)
	}
}

func TestMissingSecretKeyIsReportedClearly(t *testing.T) {
	secret := credsSecret()
	delete(secret.Data, "secret-key")
	s := testSolver(t, &fakeAPI{}, secret)

	err := s.Present(challenge(t, defaultConfig(), "_acme-challenge.reynt.co.", "reynt.co.", "tok"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), `no key "secret-key"`) {
		t.Errorf("error %q should name the missing key", err)
	}
}

func TestEmptySecretValueIsRejected(t *testing.T) {
	secret := credsSecret()
	secret.Data["api-key"] = []byte("")
	s := testSolver(t, &fakeAPI{}, secret)

	if err := s.Present(challenge(t, defaultConfig(), "_acme-challenge.reynt.co.", "reynt.co.", "tok")); err == nil {
		t.Fatal("expected an error for an empty credential")
	}
}

func TestAPIErrorIsWrappedWithContext(t *testing.T) {
	api := &fakeAPI{retrieveErr: errors.New("porkbun api: Invalid API key. (002)")}
	s := testSolver(t, api, credsSecret())

	err := s.Present(challenge(t, defaultConfig(), "_acme-challenge.reynt.co.", "reynt.co.", "tok"))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"_acme-challenge.reynt.co", "Invalid API key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}
}

// Concurrent challenges against the same domain must be serialised, both to
// avoid a read-modify-write race on the idempotency check and to halve the
// request rate against a rate-limited API.
func TestConcurrentPresentsForOneDomainAreSerialised(t *testing.T) {
	var (
		mu       sync.Mutex
		inFlight int
		maxSeen  int
	)

	api := &fakeAPI{onCall: func() {
		mu.Lock()
		inFlight++
		if inFlight > maxSeen {
			maxSeen = inFlight
		}
		mu.Unlock()

		// Hold the call open long enough that unserialised goroutines would
		// overlap observably.
		time.Sleep(2 * time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()
	}}

	s := testSolver(t, api, credsSecret())

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "token-" + string(rune('a'+i))
			if err := s.Present(challenge(t, defaultConfig(), "_acme-challenge.reynt.co.", "reynt.co.", key)); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if len(api.created) != 8 {
		t.Errorf("created %d records, want 8", len(api.created))
	}
	if maxSeen > 1 {
		t.Errorf("observed %d concurrent API calls for one domain, want 1", maxSeen)
	}
}

func TestUninitialisedSolverReportsClearly(t *testing.T) {
	s := New()
	s.newAPI = func(string, string) dnsAPI { return &fakeAPI{} }

	err := s.Present(challenge(t, defaultConfig(), "_acme-challenge.reynt.co.", "reynt.co.", "tok"))
	if err == nil || !strings.Contains(err.Error(), "not initialised") {
		t.Errorf("err = %v, want an initialisation error", err)
	}
}

func TestSolverName(t *testing.T) {
	if got := New().Name(); got != "porkbun" {
		t.Errorf("Name() = %q, want porkbun (changing it breaks every existing Issuer)", got)
	}
}
