package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	workflowservice "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/auto-scaled-workers/wci/workflow/iface"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/server/common/sdk"
)

const (
	testRegionEast = "aws-us-east-1"
	testRegionWest = "aws-us-west-2"
)

func TestHandleTaskAddSignalSelectsGroupForRegion(t *testing.T) {
	scalingConfigPayload, err := sdk.PreferProtoDataConverter.ToPayload(iface.ScalingAlgorithmConfig{})
	require.NoError(t, err)

	eastGroup := newTestScalingGroupSpec(enumspb.TASK_QUEUE_TYPE_WORKFLOW, scalingConfigPayload, nil)
	eastGroup.RegionId = testRegionEast
	spec := &iface.WorkerControllerInstanceSpec{ScalingGroupSpecs: map[string]iface.ScalingGroupSpec{
		"default": newTestScalingGroupSpec(enumspb.TASK_QUEUE_TYPE_WORKFLOW, scalingConfigPayload, nil),
		"east":    eastGroup,
	}}

	tests := []struct {
		region    string
		wantGroup string
	}{
		{testRegionEast, "east"},
		{testRegionWest, "default"},
		{"", "default"},
	}
	for _, tc := range tests {
		t.Run("region="+tc.region, func(t *testing.T) {
			algo := &deferredScalingDecisionTestAlgorithm{}
			currentDeferredScalingDecisionTestAlgorithm = algo
			t.Cleanup(func() {
				currentDeferredScalingDecisionTestAlgorithm = nil
			})

			activities := NewActivities(nil, nil, nil, tc.region)

			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestActivityEnvironment()
			env.RegisterActivity(activities.HandleTaskAddSignal)
			encodedResp, err := env.ExecuteActivity(activities.HandleTaskAddSignal, HandleTaskAddSignalActivityRequest{
				Request: newTestSignalTaskAddEvent(),
				Spec:    spec,
			})
			require.NoError(t, err)

			var resp HandleTaskAddSignalActivityResponse
			require.NoError(t, encodedResp.Get(&resp))
			assert.Equal(t, 1, algo.processCalls)
			require.Len(t, resp.Actions, 1)
			assert.Equal(t, tc.wantGroup, resp.Actions[0].ScalingGroupKey)
			assert.Contains(t, resp.UpdatedScalingStatus, tc.wantGroup)
		})
	}
}

func TestHandleDeferredScalingDecisionResolvesTaskTypesForRegion(t *testing.T) {
	scalingConfigPayload, err := sdk.PreferProtoDataConverter.ToPayload(iface.ScalingAlgorithmConfig{})
	require.NoError(t, err)

	eastGroup := newTestScalingGroupSpec(enumspb.TASK_QUEUE_TYPE_WORKFLOW, scalingConfigPayload, nil)
	eastGroup.RegionId = testRegionEast
	spec := &iface.WorkerControllerInstanceSpec{ScalingGroupSpecs: map[string]iface.ScalingGroupSpec{
		"east": eastGroup,
	}}

	tests := []struct {
		name          string
		region        string
		wantProcessed bool
	}{
		{"in region", testRegionEast, true},
		{"after failover to another region", testRegionWest, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			algo := &deferredScalingDecisionTestAlgorithm{}
			currentDeferredScalingDecisionTestAlgorithm = algo
			t.Cleanup(func() {
				currentDeferredScalingDecisionTestAlgorithm = nil
			})

			activities := NewActivities(nil, nil, nil, tc.region)

			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestActivityEnvironment()
			env.RegisterActivity(activities.HandleDeferredScalingDecision)
			_, err := env.ExecuteActivity(activities.HandleDeferredScalingDecision, HandleDeferredScalingDecisionActivityRequest{
				Request:          newTestSignalTaskAddEvent(),
				ScalingGroupKey:  "east",
				ScalingGroupSpec: eastGroup,
				Spec:             spec,
				// Stale workflow-computed value must be ignored once Spec is present.
				EffectiveTaskTypes: []enumspb.TaskQueueType{enumspb.TASK_QUEUE_TYPE_WORKFLOW},
			})
			require.NoError(t, err)
			if tc.wantProcessed {
				assert.Equal(t, 1, algo.deferredCalls)
			} else {
				assert.Equal(t, 0, algo.deferredCalls)
			}
		})
	}
}

func TestInvokeWorkersToRegisterTaskQueues_SkipsGroupsOutsideRegion(t *testing.T) {
	fake := &fakeWorkflowServiceClient{describeFn: func(*workflowservice.DescribeWorkerDeploymentVersionRequest) (*workflowservice.DescribeWorkerDeploymentVersionResponse, error) {
		return describeResponseWithTypes(), nil // nothing registered
	}}
	eastGroup := rateBasedWorkerSetGroup(t, enumspb.TASK_QUEUE_TYPE_WORKFLOW, 2)
	eastGroup.RegionId = testRegionEast
	westGroup := rateBasedWorkerSetGroup(t, enumspb.TASK_QUEUE_TYPE_WORKFLOW, 3)
	westGroup.RegionId = testRegionWest
	spec := iface.WorkerControllerInstanceSpec{ScalingGroupSpecs: map[string]iface.ScalingGroupSpec{
		"east": eastGroup,
		"west": westGroup,
	}}
	scalingStatus := map[string]iface.ScalingAlgorithmStatus{
		"east": {stateWorkerCountKey: int64(4)},
	}

	resp := runInvokeWorkersToRegisterTaskQueuesInRegion(t, fake, spec, scalingStatus, testRegionWest)

	require.Contains(t, resp.UpdatedScalingStatus, "west")
	assert.Equal(t, int64(3), resp.UpdatedScalingStatus["west"].GetInt64Field(stateWorkerCountKey, -1))
	require.Contains(t, resp.UpdatedScalingStatus, "east")
	assert.Equal(t, int64(4), resp.UpdatedScalingStatus["east"].GetInt64Field(stateWorkerCountKey, -1),
		"a group scoped to another region must not be resized, and its status must be carried forward")
}
