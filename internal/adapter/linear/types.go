package linear

import "time"

// linearIssue is the GraphQL response shape for a single issue.
type linearIssue struct {
	ID          string         `json:"id"`
	Identifier  string         `json:"identifier"` // e.g. "FOO-123"
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Priority    int            `json:"priority"`
	URL         string         `json:"url"`
	State       linearState    `json:"state"`
	Labels      linearLabels   `json:"labels"`
	Assignee    *linearUser    `json:"assignee"`
	Creator     *linearCreator `json:"creator"`
	Team        linearTeamRef  `json:"team"`
	CreatedAt   *time.Time     `json:"createdAt"`
	UpdatedAt   *time.Time     `json:"updatedAt"`
}

type linearState struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // e.g. "triage","backlog","started","completed","canceled"
}

type linearLabels struct {
	Nodes []linearLabel `json:"nodes"`
}

type linearLabel struct {
	Name string `json:"name"`
}

type linearUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type linearCreator = linearUser

type linearTeamRef struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

type linearProject struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	State       string          `json:"state"`
	Icon        string          `json:"icon"`
	Color       string          `json:"color"`
	CreatedAt   *time.Time      `json:"createdAt"`
	UpdatedAt   *time.Time      `json:"updatedAt"`
	Issues      linearIssueConn `json:"issues"`
}

type linearIssueConn struct {
	Nodes []linearIssue `json:"nodes"`
}

type linearProjectConn struct {
	Nodes []linearProject `json:"nodes"`
}

type linearInitiative struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Status      string            `json:"status"`
	CreatedAt   *time.Time        `json:"createdAt"`
	UpdatedAt   *time.Time        `json:"updatedAt"`
	Projects    linearProjectConn `json:"projects"`
}

type gqlError struct {
	Message string `json:"message"`
}

type issuesResponse struct {
	Issues linearIssueConnection `json:"issues"`
}

type linearIssueConnection struct {
	Nodes    []linearIssue  `json:"nodes"`
	PageInfo linearPageInfo `json:"pageInfo"`
}

type projectsResponse struct {
	Projects linearProjectConnection `json:"projects"`
}

type linearProjectConnection struct {
	Nodes    []linearProject `json:"nodes"`
	PageInfo linearPageInfo  `json:"pageInfo"`
}

type projectResponse struct {
	Project *linearProject `json:"project"`
}

type initiativesResponse struct {
	Initiatives linearInitiativeConnection `json:"initiatives"`
}

type linearInitiativeConnection struct {
	Nodes    []linearInitiative `json:"nodes"`
	PageInfo linearPageInfo     `json:"pageInfo"`
}

type linearPageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type updateIssueStateResponse struct {
	IssueUpdate struct {
		Success bool `json:"success"`
	} `json:"issueUpdate"`
}

type addCommentResponse struct {
	CommentCreate struct {
		Success bool `json:"success"`
	} `json:"commentCreate"`
}

type viewerResponse struct {
	Viewer struct {
		ID string `json:"id"`
	} `json:"viewer"`
}
