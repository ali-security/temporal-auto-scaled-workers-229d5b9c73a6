package iface

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
)

const (
	testRegionEast = "aws-us-east-1"
	testRegionWest = "aws-us-west-2"
)

var (
	wf  = enumspb.TASK_QUEUE_TYPE_WORKFLOW
	act = enumspb.TASK_QUEUE_TYPE_ACTIVITY
	nex = enumspb.TASK_QUEUE_TYPE_NEXUS
)

func testGroup(region string, taskTypes ...enumspb.TaskQueueType) ScalingGroupSpec {
	return ScalingGroupSpec{
		TaskTypes: taskTypes,
		RegionId:  region,
		Compute:   ComputeProviderSpec{ProviderType: ComputeProviderTypeTestInvoke},
	}
}

func TestForTaskQueueTypeRegionPrecedence(t *testing.T) {
	spec := &WorkerControllerInstanceSpec{ScalingGroupSpecs: map[string]ScalingGroupSpec{
		"any-wf":        testGroup("", wf),
		"any-catch-all": testGroup(""),
		"east-wf":       testGroup(testRegionEast, wf),
		"east-catchall": testGroup(testRegionEast),
		"west-act":      testGroup(testRegionWest, act),
	}}

	tests := []struct {
		name     string
		region   string
		taskType enumspb.TaskQueueType
		want     string
	}{
		{"region and type", testRegionEast, wf, "east-wf"},
		{"region catch-all beats region-less type", testRegionEast, act, "east-catchall"},
		{"region-less type when region has no group", testRegionWest, wf, "any-wf"},
		{"region type in other region", testRegionWest, act, "west-act"},
		{"region-less catch-all as last resort", testRegionWest, nex, "any-catch-all"},
		{"no region ignores region-scoped groups", "", act, "any-catch-all"},
		{"no region uses region-less type", "", wf, "any-wf"},
		{"unknown region falls back to region-less", "gcp-us-central1", wf, "any-wf"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := spec.ForTaskQueueType(tc.taskType, tc.region)
			require.NotNil(t, got)
			assert.Equal(t, spec.ScalingGroupSpecs[tc.want], *got)
			assert.Equal(t, tc.want, spec.ownerForTaskQueueType(tc.taskType, tc.region))
		})
	}
}

func TestForTaskQueueTypeNoMatch(t *testing.T) {
	spec := &WorkerControllerInstanceSpec{ScalingGroupSpecs: map[string]ScalingGroupSpec{
		"east-wf": testGroup(testRegionEast, wf),
	}}
	assert.Nil(t, spec.ForTaskQueueType(wf, testRegionWest))
	assert.Nil(t, spec.ForTaskQueueType(wf, ""))
	assert.Nil(t, spec.ForTaskQueueType(act, testRegionEast))

	var nilSpec *WorkerControllerInstanceSpec
	assert.Nil(t, nilSpec.ForTaskQueueType(wf, testRegionEast))
}

func TestEffectiveTaskTypesForGroup(t *testing.T) {
	spec := &WorkerControllerInstanceSpec{ScalingGroupSpecs: map[string]ScalingGroupSpec{
		"any-wf":        testGroup("", wf),
		"any-catch-all": testGroup(""),
		"east-act":      testGroup(testRegionEast, act, nex),
		"west-catchall": testGroup(testRegionWest),
	}}

	tests := []struct {
		name   string
		group  string
		region string
		want   []enumspb.TaskQueueType
	}{
		{"region-less catch-all without region", "any-catch-all", "", []enumspb.TaskQueueType{act, nex}},
		{"region-less type without region", "any-wf", "", []enumspb.TaskQueueType{wf}},
		{"region-scoped group without region", "east-act", "", []enumspb.TaskQueueType{}},
		{"region-scoped group in its region", "east-act", testRegionEast, []enumspb.TaskQueueType{act, nex}},
		{"region-less catch-all shadowed in region", "any-catch-all", testRegionEast, []enumspb.TaskQueueType{}},
		{"region-less type still serves in region", "any-wf", testRegionEast, []enumspb.TaskQueueType{wf}},
		{"region catch-all claims everything", "west-catchall", testRegionWest, []enumspb.TaskQueueType{act, nex, wf}},
		{"region-less type shadowed by region catch-all", "any-wf", testRegionWest, []enumspb.TaskQueueType{}},
		{"region-scoped group in other region", "east-act", testRegionWest, []enumspb.TaskQueueType{}},
		{"unknown group", "missing", testRegionEast, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, spec.EffectiveTaskTypesForGroup(tc.group, tc.region))
		})
	}
}

// Without region-scoped groups, results must match the pre-region behavior regardless of the cell's region.
func TestEffectiveTaskTypesForGroupWithoutRegionScopedGroups(t *testing.T) {
	spec := &WorkerControllerInstanceSpec{ScalingGroupSpecs: map[string]ScalingGroupSpec{
		"workflows": testGroup("", wf),
		"rest":      testGroup(""),
	}}
	for _, region := range []string{"", testRegionEast} {
		assert.Equal(t, []enumspb.TaskQueueType{wf}, spec.EffectiveTaskTypesForGroup("workflows", region))
		assert.Equal(t, []enumspb.TaskQueueType{act, nex}, spec.EffectiveTaskTypesForGroup("rest", region))
	}
}

func TestValidateRegionScopedGroups(t *testing.T) {
	tests := []struct {
		name    string
		groups  map[string]ScalingGroupSpec
		wantErr string
	}{
		{
			name: "same task type in different regions",
			groups: map[string]ScalingGroupSpec{
				"any-wf":  testGroup("", wf),
				"east-wf": testGroup(testRegionEast, wf),
				"west-wf": testGroup(testRegionWest, wf),
			},
		},
		{
			name: "one catch-all per region",
			groups: map[string]ScalingGroupSpec{
				"any":  testGroup(""),
				"east": testGroup(testRegionEast),
				"west": testGroup(testRegionWest),
			},
		},
		{
			name: "duplicate task type in same region",
			groups: map[string]ScalingGroupSpec{
				"a": testGroup(testRegionEast, wf),
				"b": testGroup(testRegionEast, wf, act),
			},
			wantErr: "appears in more than one entry for region " + testRegionEast,
		},
		{
			name: "duplicate task type without region",
			groups: map[string]ScalingGroupSpec{
				"a": testGroup("", wf),
				"b": testGroup("", wf),
			},
			wantErr: "appears in more than one entry",
		},
		{
			name: "two catch-alls in same region",
			groups: map[string]ScalingGroupSpec{
				"a": testGroup(testRegionEast),
				"b": testGroup(testRegionEast),
			},
			wantErr: "only one scaling group in region " + testRegionEast + " can have no task types defined",
		},
		{
			name: "two region-less catch-alls",
			groups: map[string]ScalingGroupSpec{
				"a": testGroup(""),
				"b": testGroup(""),
			},
			wantErr: "only one scaling group can have no task types defined",
		},
		{
			name: "region id with surrounding whitespace",
			groups: map[string]ScalingGroupSpec{
				"a": testGroup(" " + testRegionEast),
			},
			wantErr: "must not have leading or trailing whitespace",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := (&WorkerControllerInstanceSpec{ScalingGroupSpecs: tc.groups}).Validate()
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestBuildUpdatedSpecRegionId(t *testing.T) {
	current := &WorkerControllerInstanceSpec{ScalingGroupSpecs: map[string]ScalingGroupSpec{
		"group": testGroup(testRegionEast, wf),
	}}

	updated, err := BuildUpdatedSpec(current, &UpdateWorkerControllerInstanceRequest{
		UpsertScalingGroups: map[string]ScalingGroupSpecUpdate{
			"group": {Spec: testGroup(testRegionWest, act), UpdateMask: []string{"region_id"}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, testRegionWest, updated.ScalingGroupSpecs["group"].RegionId)
	assert.Equal(t, []enumspb.TaskQueueType{wf}, updated.ScalingGroupSpecs["group"].TaskTypes, "fields outside the mask must be untouched")
	assert.Equal(t, testRegionEast, current.ScalingGroupSpecs["group"].RegionId, "current spec must not be mutated")

	updated, err = BuildUpdatedSpec(updated, &UpdateWorkerControllerInstanceRequest{
		UpsertScalingGroups: map[string]ScalingGroupSpecUpdate{
			"group": {Spec: testGroup("", wf), UpdateMask: []string{"region_id"}},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, updated.ScalingGroupSpecs["group"].RegionId, "masked empty region id must unset it")
}

func TestCloneCopiesRegionId(t *testing.T) {
	group := testGroup(testRegionEast, wf)
	assert.Equal(t, testRegionEast, group.Clone().RegionId)
}
