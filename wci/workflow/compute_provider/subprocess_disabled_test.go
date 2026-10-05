//go:build !computeprovider_subprocess || release

package computeprovider

import (
	"context"
	"testing"

	"go.temporal.io/auto-scaled-workers/wci/workflow/iface"
)

// The subprocess compute provider runs arbitrary local commands taken from the
// user supplied compute config. It must only be compiled in when explicitly
// opted into via the computeprovider_subprocess build tag, so even enabling it
// through dynamic config must not make it available in a default build.
func TestGetComputeProvider_SubprocessNotCompiledInByDefault(t *testing.T) {
	providerConstructorsMu.RLock()
	_, registered := providerConstructors[iface.ComputeProviderTypeSubprocess]
	providerConstructorsMu.RUnlock()
	if registered {
		t.Fatalf("subprocess compute provider must not be registered without the computeprovider_subprocess build tag")
	}

	dc := newEnabledComputeProvidersCollection([]string{string(iface.ComputeProviderTypeSubprocess)})
	provider, err := GetComputeProvider(context.Background(), iface.ComputeProviderTypeSubprocess, dc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider != nil {
		t.Fatalf("expected no subprocess compute provider in a default build, got %T", provider)
	}
}
