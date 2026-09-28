//go:build !release

package computeprovider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestK8sUpdateWorkerSetSize(t *testing.T) {
	const scalePath = "/apis/apps/v1/namespaces/test-namespace/deployments/test-deployment/scale"
	scaleResponse := autoscalingv1.Scale{
		TypeMeta:   metav1.TypeMeta{APIVersion: "autoscaling/v1", Kind: "Scale"},
		ObjectMeta: metav1.ObjectMeta{Name: "test-deployment", ResourceVersion: "1"},
		Spec:       autoscalingv1.ScaleSpec{Replicas: 1},
		Status:     autoscalingv1.ScaleStatus{Replicas: 1},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != scalePath {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(scaleResponse)
		case http.MethodPut:
			var scale autoscalingv1.Scale
			if err := json.NewDecoder(r.Body).Decode(&scale); err != nil || scale.Spec.Replicas != 3 {
				http.Error(w, "invalid scale update", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(scale)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)

	kubeconfig := fmt.Sprintf(`{"apiVersion":"v1","kind":"Config","clusters":[{"name":"test","cluster":{"server":%q}}],"contexts":[{"name":"test","context":{"cluster":"test"}}],"current-context":"test"}`, server.URL)
	provider := &k8sComputeProvider{}
	require.NoError(t, provider.UpdateWorkerSetSize(t.Context(), RequestContext{}, ComputeProviderConfig{
		configK8sNamespace:  "test-namespace",
		configK8sDeployment: "test-deployment",
		configK8sKubeconfig: kubeconfig,
	}, 3))
}
