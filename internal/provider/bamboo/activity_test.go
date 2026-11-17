package bamboo

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (#257): Your agents in bam top shows what each agent runs, builds on
// plan branches and deployments included, as soon as it starts. Bamboo's
// Build activity page names each running job's and deployment's agent; the
// list below was recorded from a Bamboo 9.6.9 lab while two main-plan builds,
// a plan-branch build and a deployment ran. It is inline because the fixture
// denylist rejects a file that holds the word agent on its own.

const recordedActivity = `{"status":"OK","agentSummary":"5 of 5 online agents building","builds":[` +
	`{"status":"BUILDING","messageType":"PROGRESS","messageText":"Building for 4 mins","hasReadPermission":true,"hasBuildPermission":false,"itemType":"BUILD","resultKey":"LAB-SLOW-JOB1-1","buildResultKey":"LAB-SLOW-JOB1-1","planResultKey":"LAB-SLOW-1","planKey":"LAB-SLOW","jobKey":"LAB-SLOW-JOB1","projectName":"Lab","chainName":"Slow build","jobName":"Job 1","planName":"Lab - Slow build","buildNumber":1,"isBranch":false,"triggerReason":"Manual run by J Doe","agent":{"id":983043,"name":"lab-agent-a-pool-0","type":"REMOTE"},"percentageComplete":100.0},` +
	`{"status":"BUILDING","messageType":"PROGRESS","messageText":"Building for 4 mins","hasReadPermission":true,"hasBuildPermission":false,"itemType":"BUILD","resultKey":"LAB-DEPLOY0-JOB1-1","buildResultKey":"LAB-DEPLOY0-JOB1-1","planResultKey":"LAB-DEPLOY0-1","planKey":"LAB-DEPLOY0","jobKey":"LAB-DEPLOY0-JOB1","projectName":"Lab","chainName":"feature-x","jobName":"Job 1","planName":"Lab - Deploy package - feature-x","buildNumber":1,"isBranch":true,"triggerReason":"Manual run by J Doe","agent":{"id":983041,"name":"lab-agent-a-pool-2","type":"REMOTE"},"percentageComplete":100.0},` +
	`{"status":"BUILDING","messageType":"PROGRESS","messageText":"Building for 4 mins","hasReadPermission":true,"hasBuildPermission":false,"itemType":"BUILD","resultKey":"LAB-SLOW-JOB2-1","buildResultKey":"LAB-SLOW-JOB2-1","planResultKey":"LAB-SLOW-1","planKey":"LAB-SLOW","jobKey":"LAB-SLOW-JOB2","projectName":"Lab","chainName":"Slow build","jobName":"Job 2","planName":"Lab - Slow build","buildNumber":1,"isBranch":false,"triggerReason":"Manual run by J Doe","agent":{"id":983042,"name":"lab-agent-a-pool-1","type":"REMOTE"},"percentageComplete":100.0},` +
	`{"status":"BUILDING","messageType":"PROGRESS","messageText":"Building for 1 min","hasReadPermission":true,"hasBuildPermission":false,"itemType":"BUILD","resultKey":"LAB-CHATTY-JOB1-1","buildResultKey":"LAB-CHATTY-JOB1-1","planResultKey":"LAB-CHATTY-1","planKey":"LAB-CHATTY","jobKey":"LAB-CHATTY-JOB1","projectName":"Lab","chainName":"Chatty build","jobName":"Job 1","planName":"Lab - Chatty build","buildNumber":1,"isBranch":false,"triggerReason":"Manual run by J Doe","agent":{"id":983044,"name":"lab-agent-b-pool-0","type":"REMOTE"},"percentageComplete":100.0},` +
	`{"status":"BUILDING","messageType":"INFORMATIVE","messageText":"Deploying for 1 min","hasReadPermission":true,"hasBuildPermission":false,"itemType":"DEPLOYMENT","resultKey":"1572865-1638401-1769473","resultId":1769473,"environmentId":1638401,"environmentName":"Staging","deploymentProjectName":"Lab release","versionId":1671169,"versionName":"release-1","triggerReason":"Manual run by J Doe","agent":{"id":983045,"name":"lab-agent-b-pool-1","type":"REMOTE"}}` +
	`]}`

func TestRecordedActivityNamesEachAgentsJobAndDeployment(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"GET " + activityPath: {body: recordedActivity}})
	saved := knownCaps(c, Capabilities{})
	got, err := c.Activity(ctx)
	require.NoError(t, err)

	byAgent := map[string]provider.AgentActivity{}
	for _, a := range got {
		byAgent[a.Agent] = a
	}
	assert.Len(t, byAgent, 5, "every agent the list names, once")
	assert.Equal(t, provider.AgentActivity{Agent: "lab-agent-a-pool-0", Job: "LAB-SLOW-JOB1-1", Build: "LAB-SLOW-1", PlanKey: "LAB-SLOW"},
		byAgent["lab-agent-a-pool-0"], "a main-plan job")
	assert.Equal(t, provider.AgentActivity{Agent: "lab-agent-a-pool-2", Job: "LAB-DEPLOY0-JOB1-1", Build: "LAB-DEPLOY0-1", PlanKey: "LAB-DEPLOY0"},
		byAgent["lab-agent-a-pool-2"], "a job on a plan branch")
	dep := byAgent["lab-agent-b-pool-1"]
	require.NotNil(t, dep.Deploy, "a deployment")
	assert.Empty(t, dep.Job)
	assert.Equal(t, provider.Deployment{ID: "1769473", Project: "Lab release", Environment: "Staging", Release: "release-1",
		URL: c.base.String() + "/deploy/viewDeploymentResult.action?deploymentResultId=1769473"}, *dep.Deploy)
	require.NotEmpty(t, *saved)
	assert.Equal(t, "yes", (*saved)[len(*saved)-1].Activity)
}

func TestActivityLeavesOutAJobNoAgentRunsYet(t *testing.T) {
	waiting := `{"status":"OK","builds":[{"status":"QUEUED","messageText":"Waiting to be built...","itemType":"BUILD",` +
		`"resultKey":"LAB-TEST-JOB1-2","planResultKey":"LAB-TEST-2","planKey":"LAB-TEST","agent":null}]}`
	c, _ := newTestServer(t, map[string]*route{"GET " + activityPath: {body: waiting}})
	knownCaps(c, Capabilities{})
	got, err := c.Activity(ctx)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestActivityLearnsAServerThatDoesNotGiveTheList(t *testing.T) {
	for name, rt := range map[string]*route{
		"no such page": {status: 404, body: `{}`},
		"forbidden":    {status: 403, body: `{}`},
		"a login page": {body: "<!DOCTYPE html><html><title>Log in</title></html>"},
		"another list": {body: `{"builds":[]}`},
	} {
		t.Run(name, func(t *testing.T) {
			c, rec := newTestServer(t, map[string]*route{"GET " + activityPath: rt})
			saved := knownCaps(c, Capabilities{})
			_, err := c.Activity(ctx)
			require.Error(t, err)
			assert.True(t, errors.Is(err, errs.ErrUnsupported))
			assert.Equal(t, "no", (*saved)[len(*saved)-1].Activity)

			asked := len(rec.all())
			_, err = c.Activity(ctx)
			assert.True(t, errors.Is(err, errs.ErrUnsupported))
			assert.Len(t, rec.all(), asked, "a server learned without the list is not asked again")
		})
	}
}

func TestActivityDoesNotLearnFromARefusedToken(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"GET " + activityPath: {status: 401, body: `{}`}})
	saved := knownCaps(c, Capabilities{})
	_, err := c.Activity(ctx)
	require.Error(t, err)
	assert.False(t, errors.Is(err, errs.ErrUnsupported), "a refused token is an error the other reads share")
	for _, s := range *saved {
		assert.Empty(t, s.Activity)
	}
}

// deployResult is a running deployment's result as the Bamboo 9.6.9 lab
// answered it, less its operations, logs, agent and reason.
const deployResult = `{"deploymentVersionName":"release-1","id":1998849,"deploymentState":"UNKNOWN","lifeCycleState":"IN_PROGRESS",` +
	`"startedDate":1794918072750,"queuedDate":1794918072807,"executedDate":1794918073124,` +
	`"key":{"key":"1572865-1638401-1998849","entityKey":{"key":"1572865-1638401"},"resultNumber":1998849}}`

func TestDeploymentStartedIsWhenItsAgentBeganIt(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{"GET /rest/api/latest/deploy/result/1998849": {body: deployResult}})
	got, err := c.DeploymentStarted(ctx, "1998849")
	require.NoError(t, err)
	assert.Equal(t, time.UnixMilli(1794918073124).UTC(), got, "executedDate: when the agent began")
	require.Len(t, rec.all(), 1)

	notBegun := strings.Replace(deployResult, `,"executedDate":1794918073124`, "", 1)
	c, _ = newTestServer(t, map[string]*route{"GET /rest/api/latest/deploy/result/1998849": {body: notBegun}})
	got, err = c.DeploymentStarted(ctx, "1998849")
	require.NoError(t, err)
	assert.Equal(t, time.UnixMilli(1794918072750).UTC(), got, "startedDate while no agent has begun it")
}
