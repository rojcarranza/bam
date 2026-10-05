package bamboo

// Bamboo JSON shapes. Field names follow Atlassian's REST documentation and
// are provisional until TestRecordedShapesDecode runs against recordings from
// a Bamboo Data Center 9.x server (testdata/recorded).

type userDTO struct {
	Name     string `json:"name"`
	FullName string `json:"fullName"`
}

type infoDTO struct {
	Version string `json:"version"`
}

type projectDTO struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type planDTO struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	ShortName  string `json:"shortName"`
	ProjectKey string `json:"projectKey"`
}

type branchDTO struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	ShortName string `json:"shortName"`
}

type resultPlanDTO struct {
	Key       string `json:"key"`
	ShortName string `json:"shortName"`
	Master    *struct {
		Key string `json:"key"`
	} `json:"master"`
}

type resultDTO struct {
	Key                    string        `json:"key"`
	BuildResultKey         string        `json:"buildResultKey"`
	BuildNumber            int           `json:"buildNumber"`
	State                  string        `json:"state"`
	BuildState             string        `json:"buildState"`
	LifeCycleState         string        `json:"lifeCycleState"`
	NotRunYet              bool          `json:"notRunYet"`
	BuildStartedTime       string        `json:"buildStartedTime"`
	BuildCompletedTime     string        `json:"buildCompletedTime"`
	BuildDuration          int64         `json:"buildDuration"`
	BuildDurationInSeconds int64         `json:"buildDurationInSeconds"`
	BuildReason            string        `json:"buildReason"`
	ReasonSummary          string        `json:"reasonSummary"`
	VcsRevisionKey         string        `json:"vcsRevisionKey"`
	Plan                   resultPlanDTO `json:"plan"`
	Labels                 struct {
		Label []struct {
			Name string `json:"name"`
		} `json:"label"`
	} `json:"labels"`
	VcsRevisions struct {
		VcsRevision []struct {
			RepositoryName string `json:"repositoryName"`
			VcsRevisionKey string `json:"vcsRevisionKey"`
		} `json:"vcsRevision"`
	} `json:"vcsRevisions"`
	Stages struct {
		Stage []stageDTO `json:"stage"`
	} `json:"stages"`
}

type stageDTO struct {
	Name           string `json:"name"`
	State          string `json:"state"`
	LifeCycleState string `json:"lifeCycleState"`
	Results        struct {
		Result []resultDTO `json:"result"`
	} `json:"results"`
}

// statusDTO is /result/status/{key}: a running build's progress.
type statusDTO struct {
	CurrentStage string `json:"currentStage"`
	Finished     bool   `json:"finished"`
	Progress     *struct {
		IsValid              bool    `json:"isValid"`
		AverageBuildDuration int64   `json:"averageBuildDuration"`
		BuildTime            int64   `json:"buildTime"`
		PercentageCompleted  float64 `json:"percentageCompleted"`
	} `json:"progress"`
}
