//go:build !release

package computeprovider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func TestUpdateK8sDeploymentScale(t *testing.T) {
	const scalePath = "/apis/apps/v1/namespaces/test-namespace/deployments/test-deployment/scale"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != scalePath {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = fmt.Fprint(w, `{"apiVersion":"autoscaling/v1","kind":"Scale","metadata":{"name":"test-deployment","resourceVersion":"1"},"spec":{"replicas":1}}`)
		case http.MethodPut:
			var scale struct {
				Spec struct {
					Replicas int32 `json:"replicas"`
				} `json:"spec"`
			}
			if err := json.NewDecoder(r.Body).Decode(&scale); err != nil || scale.Spec.Replicas != 3 {
				http.Error(w, "invalid scale update", http.StatusBadRequest)
				return
			}
			_, _ = fmt.Fprint(w, `{"apiVersion":"autoscaling/v1","kind":"Scale","metadata":{"name":"test-deployment","resourceVersion":"1"},"spec":{"replicas":3}}`)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)

	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	deployments := client.Resource(k8sDeploymentsResource).Namespace("test-namespace")

	require.NoError(t, updateK8sDeploymentScale(t.Context(), deployments, "test-deployment", 3))
}
