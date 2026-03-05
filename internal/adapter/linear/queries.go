package linear

// Issue fields included in all issue queries.
// Expanded inline in each query below for explicitness.

const queryAssignedIssues = `
query AssignedIssues($teamId: String!, $assigneeId: String!) {
	issues(filter: {
		team: { id: { eq: $teamId } }
		assignee: { id: { eq: $assigneeId } }
		state: { type: { nin: ["completed", "cancelled"] } }
	}) {
		nodes {
			id identifier title description priority url
			state { id name type }
			labels { nodes { name } }
			assignee { id name }
			team { id key }
			createdAt updatedAt
		}
	}
}`

const queryIssueByID = `
query IssueByID($id: String!) {
	issue(id: $id) {
		id identifier title description priority url
		state { id name type }
		labels { nodes { name } }
		assignee { id name }
		team { id key }
		createdAt updatedAt
	}
}`

const queryIssuesByIDs = `
query IssuesByIDs($ids: [String!]!) {
	issues(filter: { id: { in: $ids } }) {
		nodes {
			id identifier title description priority url
			state { id name type }
			labels { nodes { name } }
			assignee { id name }
			team { id key }
			createdAt updatedAt
		}
	}
}`

const queryTeamIssues = `
query TeamIssues($teamId: String!, $filter: String) {
	issues(filter: {
		team: { id: { eq: $teamId } }
		title: { containsIgnoreCase: $filter }
		state: { type: { nin: ["completed", "cancelled"] } }
	}) {
		nodes {
			id identifier title description priority url
			state { id name type }
			labels { nodes { name } }
			assignee { id name }
			team { id key }
			createdAt updatedAt
		}
	}
}`

const queryProjects = `
query Projects($teamId: String!) {
	projects(filter: {
		accessibleTeams: { id: { eq: $teamId } }
		state: { nin: ["completed", "cancelled"] }
	}) {
		nodes {
			id name description state icon color
			issues {
				nodes {
					id identifier title description
					state { id name }
					labels { nodes { name } }
					team { id key }
					createdAt updatedAt
				}
			}
		}
	}
}`

const queryProjectWithIssues = `
query ProjectWithIssues($id: String!) {
	project(id: $id) {
		id name description state icon color
		issues(filter: { state: { type: { nin: ["completed", "cancelled"] } } }) {
			nodes {
				id identifier title description
				state { id name }
				labels { nodes { name } }
				team { id key }
				createdAt updatedAt
			}
		}
	}
}`

const queryInitiatives = `
query Initiatives {
	initiatives(filter: { status: { nin: ["completed", "cancelled"] } }) {
		nodes {
			id name description status
			projects {
				nodes {
					id name description
					issues {
						nodes {
							id identifier title description
							state { id name }
							labels { nodes { name } }
							team { id key }
							createdAt updatedAt
						}
					}
				}
			}
		}
	}
}`

const querySingleInitiative = `
query SingleInitiative($id: String!) {
	initiative(id: $id) {
		id name description status
		projects {
			nodes {
				id name description
				issues {
					nodes {
						id identifier title description
						state { id name }
						labels { nodes { name } }
						team { id key }
						createdAt updatedAt
					}
				}
			}
		}
	}
}`

const queryViewer = `
query Viewer {
	viewer {
		id
	}
}`

const mutationUpdateIssueState = `
mutation UpdateIssueState($issueId: String!, $stateId: String!) {
	issueUpdate(id: $issueId, input: { stateId: $stateId }) {
		success
	}
}`

const mutationAddComment = `
mutation AddComment($issueId: String!, $body: String!) {
	commentCreate(input: { issueId: $issueId, body: $body }) {
		success
	}
}`
