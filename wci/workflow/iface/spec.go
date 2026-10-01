package iface

import (
	"maps"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
)

type (
	ComputeProviderType string

	// ComputeProviderSpec is a single provider type and its settings (used per task queue type or as default).
	ComputeProviderSpec struct {
		ProviderType  ComputeProviderType `json:"provider_type,omitempty"`
		Config        *commonpb.Payload   `json:"config,omitempty"`
		NexusEndpoint string              `json:"nexus_endpoint,omitempty"`
	}

	ScalingAlgorithmType   string
	ScalingAlgorithmConfig map[string]any

	// ScalingAlgorithmSpec is a single scaling algorithm and its settings (used per task queue type or as default).
	ScalingAlgorithmSpec struct {
		ScalingAlgorithm ScalingAlgorithmType `json:"scaling_algorithm,omitempty"`
		Config           *commonpb.Payload    `json:"config,omitempty"`
	}

	// ScalingGroupSpec is one entry: a list of task types (and optionally a region) and the compute/scaling spec that applies to them.
	ScalingGroupSpec struct {
		TaskTypes []enumspb.TaskQueueType `json:"task_types"`
		// RegionId restricts the group to worker controllers hosted in this region.
		RegionId string                `json:"region_id,omitempty"`
		Compute  ComputeProviderSpec   `json:"compute"`
		Scaling  *ScalingAlgorithmSpec `json:"scaling,omitempty"`
	}

	ScalingGroupSpecUpdate struct {
		Spec       ScalingGroupSpec `json:"spec"`
		UpdateMask []string         `json:"update_mask"`
	}

	// WorkerControllerInstanceSpec contains the individual scaling group specs
	WorkerControllerInstanceSpec struct {
		ScalingGroupSpecs map[string]ScalingGroupSpec `json:"scaling_group_specs"`
	}
)

const (
	ComputeProviderTypeAWSLambda     ComputeProviderType = "aws-lambda"
	ComputeProviderTypeAWSAgentCore  ComputeProviderType = "aws-agentcore"
	ComputeProviderTypeAWSECS        ComputeProviderType = "aws-ecs"
	ComputeProviderTypeSubprocess    ComputeProviderType = "subprocess"
	ComputeProviderTypeK8s           ComputeProviderType = "k8s"
	ComputeProviderTypeGCPCloudRun   ComputeProviderType = "gcp-cloud-run"
	ComputeProviderTypeTestInvoke    ComputeProviderType = "test-invoke"
	ComputeProviderTypeTestWorkerSet ComputeProviderType = "test-worker-set"

	ScalingAlgorithmNoSync    ScalingAlgorithmType = "no-sync"
	ScalingAlgorithmRateBased ScalingAlgorithmType = "rate-based"
)

var validComputeProviderTypes = map[string]ComputeProviderType{
	string(ComputeProviderTypeAWSLambda):     ComputeProviderTypeAWSLambda,
	string(ComputeProviderTypeAWSAgentCore):  ComputeProviderTypeAWSAgentCore,
	string(ComputeProviderTypeAWSECS):        ComputeProviderTypeAWSECS,
	string(ComputeProviderTypeSubprocess):    ComputeProviderTypeSubprocess,
	string(ComputeProviderTypeK8s):           ComputeProviderTypeK8s,
	string(ComputeProviderTypeGCPCloudRun):   ComputeProviderTypeGCPCloudRun,
	string(ComputeProviderTypeTestInvoke):    ComputeProviderTypeTestInvoke,
	string(ComputeProviderTypeTestWorkerSet): ComputeProviderTypeTestWorkerSet,
}

var validScalingAlgorithmTypes = map[string]ScalingAlgorithmType{
	string(ScalingAlgorithmNoSync):    ScalingAlgorithmNoSync,
	string(ScalingAlgorithmRateBased): ScalingAlgorithmRateBased,
}

// ValidComputeProviderType returns true if s is a valid enum value, and false otherwise.
func ValidComputeProviderType(s string) bool {
	if _, ok := validComputeProviderTypes[s]; ok {
		return true
	}
	return false
}

// ValidScalingAlgorithmType returns true  if s is a valid enum value, and false otherwise.
func ValidScalingAlgorithmType(s string) bool {
	if _, ok := validScalingAlgorithmTypes[s]; ok {
		return true
	}
	return false
}

var scalableTaskQueueTypes = []enumspb.TaskQueueType{enumspb.TASK_QUEUE_TYPE_ACTIVITY, enumspb.TASK_QUEUE_TYPE_NEXUS, enumspb.TASK_QUEUE_TYPE_WORKFLOW}

// ownerForTaskQueueType returns the ID of the scaling group serving t in regionId, or "" if none.
// Precedence: (region, type) > (region, catch-all) > (any region, type) > (any region, catch-all).
func (c *WorkerControllerInstanceSpec) ownerForTaskQueueType(t enumspb.TaskQueueType, regionId string) string {
	if c == nil {
		return ""
	}
	regions := []string{""}
	if regionId != "" {
		regions = []string{regionId, ""}
	}
	// Validate guarantees at most one candidate per tier; sorted keys keep this deterministic regardless.
	keys := slices.Sorted(maps.Keys(c.ScalingGroupSpecs))
	for _, region := range regions {
		for _, k := range keys {
			if v := c.ScalingGroupSpecs[k]; v.RegionId == region && slices.Contains(v.TaskTypes, t) {
				return k
			}
		}
		for _, k := range keys {
			if v := c.ScalingGroupSpecs[k]; v.RegionId == region && len(v.TaskTypes) == 0 {
				return k
			}
		}
	}
	return ""
}

// ForTaskQueueType returns the ScalingGroupSpec serving the given task queue type in regionId. Returns nil if none applies.
func (c *WorkerControllerInstanceSpec) ForTaskQueueType(t enumspb.TaskQueueType, regionId string) *ScalingGroupSpec {
	owner := c.ownerForTaskQueueType(t, regionId)
	if owner == "" {
		return nil
	}
	localCopy := c.ScalingGroupSpecs[owner]
	return &localCopy
}

// EffectiveTaskTypesForGroup returns the task types the group serves in regionId. Empty if the group
// is scoped to another region or fully shadowed by more specific groups.
func (c *WorkerControllerInstanceSpec) EffectiveTaskTypesForGroup(scalingGroupId string, regionId string) []enumspb.TaskQueueType {
	if c == nil {
		return nil
	}

	scalingGroup, ok := c.ScalingGroupSpecs[scalingGroupId]
	if !ok {
		return nil
	}

	candidates := scalingGroup.TaskTypes
	if len(candidates) == 0 {
		candidates = scalableTaskQueueTypes
	}

	effective := []enumspb.TaskQueueType{}
	for _, t := range candidates {
		if c.ownerForTaskQueueType(t, regionId) == scalingGroupId {
			effective = append(effective, t)
		}
	}
	return effective
}

// ScalingSpecForTaskQueueType returns the ScalingAlgorithmSpec for the given task queue type in regionId. Returns nil if task queue type is not found or Scaling is nil.
func (c *WorkerControllerInstanceSpec) ScalingSpecForTaskQueueType(t enumspb.TaskQueueType, regionId string) *ScalingAlgorithmSpec {
	taskQueueTypeSpec := c.ForTaskQueueType(t, regionId)
	if taskQueueTypeSpec == nil {
		return nil
	}
	return taskQueueTypeSpec.Scaling
}

// Validate ensures at least one entry, no duplicate task types per region, and each entry has valid spec.
func (c *WorkerControllerInstanceSpec) Validate() error {
	if c == nil {
		return serviceerror.NewInvalidArgumentf("spec must be provided")
	}
	if len(c.ScalingGroupSpecs) == 0 {
		return serviceerror.NewInvalidArgumentf("spec must have at least one entry")
	}

	type regionTaskType struct {
		region   string
		taskType enumspb.TaskQueueType
	}
	seen := make(map[regionTaskType]struct{})
	seenTaskTypeCatchAll := make(map[string]struct{})
	for k, v := range c.ScalingGroupSpecs {
		if len(k) == 0 {
			return serviceerror.NewInvalidArgument("scaling groups without an ID are not supported")
		}
		if v.RegionId != strings.TrimSpace(v.RegionId) {
			return serviceerror.NewInvalidArgumentf("entry %s: region id '%s' must not have leading or trailing whitespace", k, v.RegionId)
		}
		if len(v.TaskTypes) == 0 {
			if _, ok := seenTaskTypeCatchAll[v.RegionId]; ok {
				if v.RegionId == "" {
					return serviceerror.NewInvalidArgumentf("entry %s: only one scaling group can have no task types defined", k)
				}
				return serviceerror.NewInvalidArgumentf("entry %s: only one scaling group in region %s can have no task types defined", k, v.RegionId)
			}
			seenTaskTypeCatchAll[v.RegionId] = struct{}{}
		}
		for _, t := range v.TaskTypes {
			key := regionTaskType{region: v.RegionId, taskType: t}
			if _, ok := seen[key]; ok {
				if v.RegionId == "" {
					return serviceerror.NewInvalidArgumentf("entry %s: task type %s appears in more than one entry", k, t.String())
				}
				return serviceerror.NewInvalidArgumentf("entry %s: task type %s appears in more than one entry for region %s", k, t.String(), v.RegionId)
			}
			if t == enumspb.TASK_QUEUE_TYPE_UNSPECIFIED {
				return serviceerror.NewInvalidArgumentf("entry %s: task type undefined not allowed in compute spec", k)
			}
			seen[key] = struct{}{}
		}
		if !ValidComputeProviderType(string(v.Compute.ProviderType)) {
			return serviceerror.NewInvalidArgumentf("entry %s: invalid compute provider type '%s'", k, v.Compute.ProviderType)
		}
		if v.Scaling != nil {
			if !ValidScalingAlgorithmType(string(v.Scaling.ScalingAlgorithm)) {
				return serviceerror.NewInvalidArgumentf("entry %s: invalid scaling algorithm type '%s'", k, v.Scaling.ScalingAlgorithm)
			}
		}
	}
	return nil
}

func (config ScalingAlgorithmConfig) GetInt64Field(key string, defaultValue int64) int64 {
	return getInt64FromMap(config, key, defaultValue)
}

func (config ScalingAlgorithmConfig) ValidateInt64Field(key string, minValidValue int64) error {
	return validateInt64InMap(config, key, minValidValue)
}

func (config ScalingAlgorithmConfig) GetFloat64Field(key string, defaultValue float64) float64 {
	return getFloat64FromMap(config, key, defaultValue)
}

func (config ScalingAlgorithmConfig) ValidateFloat64Field(key string, minValidValue float64) error {
	return validateFloat64InMap(config, key, minValidValue)
}

func (c *ScalingGroupSpec) Clone() *ScalingGroupSpec {
	cloned := &ScalingGroupSpec{
		TaskTypes: slices.Clone(c.TaskTypes),
		RegionId:  c.RegionId,
		Compute: ComputeProviderSpec{
			ProviderType:  c.Compute.ProviderType,
			NexusEndpoint: c.Compute.NexusEndpoint,
		},
	}

	if c.Compute.Config != nil {
		cloned.Compute.Config = proto.Clone(c.Compute.Config).(*commonpb.Payload)
	}
	if c.Scaling != nil {
		clonedScaling := *c.Scaling
		cloned.Scaling = &clonedScaling
		if c.Scaling.Config != nil {
			cloned.Scaling.Config = proto.Clone(c.Scaling.Config).(*commonpb.Payload)
		}
	}

	return cloned
}
