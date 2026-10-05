package computeprovider

import (
	"context"
	"testing"

	"go.temporal.io/auto-scaled-workers/wci/client"
	"go.temporal.io/auto-scaled-workers/wci/workflow/iface"
	"go.temporal.io/server/common/dynamicconfig"
	"go.temporal.io/server/common/log"
)

func newEnabledComputeProvidersCollection(enabled []string) *dynamicconfig.Collection {
	staticConfig := map[dynamicconfig.Key]any{
		client.WorkerControllerEnabledComputeProviders.Key(): enabled,
	}
	return dynamicconfig.NewCollection(dynamicconfig.StaticClient(staticConfig), log.NewNoopLogger())
}

// When no compute providers are explicitly enabled, no compute provider may be
// instantiated: an unset configuration must not mean "everything is enabled".
func TestGetComputeProvider_NoEnabledProvidersConfigured(t *testing.T) {
	dc := dynamicconfig.NewNoopCollection()
	for _, providerType := range []iface.ComputeProviderType{
		iface.ComputeProviderTypeTestInvoke,
		iface.ComputeProviderTypeTestWorkerSet,
		iface.ComputeProviderTypeSubprocess,
	} {
		provider, err := GetComputeProvider(context.Background(), providerType, dc)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", providerType, err)
		}
		if provider != nil {
			t.Fatalf("%s: expected no compute provider when none are enabled, got %T", providerType, provider)
		}
	}
}

func TestGetComputeProvider_EmptyEnabledProvidersList(t *testing.T) {
	dc := newEnabledComputeProvidersCollection([]string{})
	provider, err := GetComputeProvider(context.Background(), iface.ComputeProviderTypeTestInvoke, dc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider != nil {
		t.Fatalf("expected no compute provider when the enabled list is empty, got %T", provider)
	}
}

func TestGetComputeProvider_OnlyExplicitlyEnabledProviders(t *testing.T) {
	dc := newEnabledComputeProvidersCollection([]string{string(iface.ComputeProviderTypeTestInvoke)})

	provider, err := GetComputeProvider(context.Background(), iface.ComputeProviderTypeTestInvoke, dc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Fatalf("expected the enabled %q compute provider to be returned", iface.ComputeProviderTypeTestInvoke)
	}

	for _, providerType := range []iface.ComputeProviderType{
		iface.ComputeProviderTypeTestWorkerSet,
		iface.ComputeProviderTypeSubprocess,
	} {
		provider, err := GetComputeProvider(context.Background(), providerType, dc)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", providerType, err)
		}
		if provider != nil {
			t.Fatalf("%s: expected no compute provider for a type that is not enabled, got %T", providerType, provider)
		}
	}
}
