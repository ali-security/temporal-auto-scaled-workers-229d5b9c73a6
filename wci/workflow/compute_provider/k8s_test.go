//go:build !release

package computeprovider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestK8sUpdateWorkerSetSizePreservesUnknownScaleFields(t *testing.T) {
	const scalePath = "/apis/apps/v1/namespaces/test-namespace/deployments/test-deployment/scale"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != scalePath {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"apiVersion":"autoscaling/v1","kind":"Scale","metadata":{"name":"test-deployment","namespace":"test-namespace"},"spec":{"replicas":1,"unknownField":"preserve-me"},"status":{"replicas":1}}`))
		case http.MethodPut:
			var scale map[string]any
			if err := json.NewDecoder(r.Body).Decode(&scale); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			spec, ok := scale["spec"].(map[string]any)
			if !ok || spec["unknownField"] != "preserve-me" {
				http.Error(w, "unknown scale field was not preserved", http.StatusBadRequest)
				return
			}
			if spec["replicas"] != float64(3) {
				http.Error(w, "replica count was not updated", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(scale)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)

	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user: {}
`, server.URL)
	provider := &k8sComputeProvider{}
	err := provider.UpdateWorkerSetSize(t.Context(), RequestContext{}, ComputeProviderConfig{
		configK8sNamespace:  "test-namespace",
		configK8sDeployment: "test-deployment",
		configK8sKubeconfig: kubeconfig,
	}, 3)
	require.NoError(t, err)
}
