package computeprovider

import (
	"context"
	"errors"
	"fmt"

	modal "github.com/modal-labs/modal-client/go"

	"go.temporal.io/auto-scaled-workers/wci/workflow/iface"
	"go.temporal.io/server/common/dynamicconfig"
)

const (
	configModalApp         = "app"
	configModalFunction    = "function"
	configModalEnvironment = "environment"
)

// reservedModalConfigKeys address the Modal function itself; every other config key is
// forwarded to the function as a keyword argument.
var reservedModalConfigKeys = map[string]struct{}{
	configModalApp:         {},
	configModalFunction:    {},
	configModalEnvironment: {},
}

type modalComputeProvider struct{}

// modalFunction is the narrow seam the provider depends on; satisfied by *modal.Function.
// A package-level resolver var lets tests swap it out without reaching Modal (mirrors
// aws_lambda.go's lambdaAPI + newLambdaClientFn).
type modalFunction interface {
	Spawn(ctx context.Context, args []any, kwargs map[string]any) (*modal.FunctionCall, error)
}

func init() {
	RegisterComputeProvider(iface.ComputeProviderTypeModal, NewModalComputeProvider)
}

// NewModalComputeProvider constructs the Modal provider. Credentials come from the
// MODAL_TOKEN_ID/MODAL_TOKEN_SECRET env vars read by modal.NewClient, so no dynamic
// config is needed.
func NewModalComputeProvider(_ context.Context, _ *dynamicconfig.Collection) (ComputeProvider, error) {
	return &modalComputeProvider{}, nil
}

func (p *modalComputeProvider) LaunchStrategy() LaunchStrategy {
	return LaunchStrategyInvoke
}

func (p *modalComputeProvider) ValidateConfig(ctx context.Context, rc RequestContext, cfg ComputeProviderConfig) error {
	// Resolving the function also probes reachability and existence (like Lambda's GetFunction).
	fn, closer, err := resolveModalFunctionFn(ctx, rc, cfg)
	if err != nil {
		return fmt.Errorf("cannot access the compute resource: %w", err)
	}
	defer closer()
	_ = fn
	return nil
}

func (p *modalComputeProvider) InvokeWorker(ctx context.Context, rc RequestContext, cfg ComputeProviderConfig) error {
	err := p.invokeWorker(ctx, rc, cfg)
	return NewProviderError(classifyModalFailure(err), err)
}

func (p *modalComputeProvider) invokeWorker(ctx context.Context, rc RequestContext, cfg ComputeProviderConfig) error {
	fn, closer, err := resolveModalFunctionFn(ctx, rc, cfg)
	if err != nil {
		return err
	}
	defer closer()

	// Non-reserved config keys are the function's kwargs, so the worker's knobs (task
	// queue, lifetime, concurrency) live in the WDV's compute config. Identity is added
	// last so it cannot be overridden.
	kwargs := map[string]any{}
	for k, v := range cfg {
		if _, reserved := reservedModalConfigKeys[k]; !reserved {
			kwargs[k] = v
		}
	}
	kwargs["namespace"] = rc.NamespaceName
	kwargs["deployment_name"] = rc.DeploymentName
	kwargs["build_id"] = rc.DeploymentBuildID

	// Spawn is fire-and-forget: it returns once Modal accepts the invocation, mirroring
	// Lambda's async InvocationType: Event. We discard the FunctionCall handle.
	if _, err := fn.Spawn(ctx, nil, kwargs); err != nil {
		return fmt.Errorf("failed to spawn modal function: %w", err)
	}
	return nil
}

func (p *modalComputeProvider) UpdateWorkerSetSize(_ context.Context, _ RequestContext, _ ComputeProviderConfig, _ int32) error {
	return errors.ErrUnsupported
}

// modalTarget resolves the Modal function to spawn. By default a worker deployment
// version maps onto Modal's own namespacing — the deployment name is the app and the
// build ID is the function within it, so a new build is a new function. Either half can
// be pinned in the config when the Modal names don't mirror the WDV.
func modalTarget(rc RequestContext, cfg ComputeProviderConfig) (appName, functionName string) {
	appName, _ = cfg[configModalApp].(string)
	if appName == "" {
		appName = rc.DeploymentName
	}
	functionName, _ = cfg[configModalFunction].(string)
	if functionName == "" {
		functionName = rc.DeploymentBuildID
	}
	return appName, functionName
}

// resolveModalFunctionFn builds a Modal client and resolves the target Function. It is a
// package-level variable so tests can swap it for a fake without reaching Modal. The
// returned closer releases the client and must always be called.
var resolveModalFunctionFn = resolveModalFunction

func resolveModalFunction(ctx context.Context, rc RequestContext, cfg ComputeProviderConfig) (modalFunction, func(), error) {
	appName, functionName := modalTarget(rc, cfg)
	if appName == "" || functionName == "" {
		return nil, nil, fmt.Errorf("modal compute provider requires %q and %q, or a deployment name and build ID to derive them from", configModalApp, configModalFunction)
	}
	environment, _ := cfg[configModalEnvironment].(string)

	client, err := modal.NewClient()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create modal client: %w", err)
	}

	fn, err := client.Functions.FromName(ctx, appName, functionName, &modal.FunctionFromNameParams{Environment: environment})
	if err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("failed to look up modal function %q/%q: %w", appName, functionName, err)
	}
	return fn, client.Close, nil
}

func classifyModalFailure(err error) FailureClass {
	if err == nil {
		return FailureUnclassified
	}
	var notFound modal.NotFoundError
	if errors.As(err, &notFound) {
		return FailureNotFound
	}
	var invalid modal.InvalidError
	if errors.As(err, &invalid) {
		return FailureRejected
	}
	return FailureUnclassified
}
