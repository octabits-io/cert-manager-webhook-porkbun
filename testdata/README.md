# Conformance test setup

The conformance suite performs real DNS changes against a real domain. Use a
throwaway domain, not one serving production traffic.

1. Create an API key at <https://porkbun.com/account/api>.

2. Enable API access **for the specific domain**: Porkbun disables it per
   domain by default, under Domain Management → Details → API Access. Skipping
   this is the single most common cause of `Invalid domain` errors, and it is
   not obvious from the API response.

3. Fill in the credentials:

   ```bash
   cp testdata/porkbun/porkbun-api-credentials.yaml.sample \
      testdata/porkbun/porkbun-api-credentials.yaml
   $EDITOR testdata/porkbun/porkbun-api-credentials.yaml
   ```

   The filled-in file is gitignored.

4. Install the envtest control-plane binaries the suite needs:

   ```bash
   go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
   export KUBEBUILDER_ASSETS=$(setup-envtest use -p path)
   ```

5. Run it (note the trailing dot on the zone):

   ```bash
   make test-conformance TEST_ZONE_NAME=example.com.
   ```

The suite takes several minutes: it waits for records to become visible on
Porkbun's authoritative nameservers.
