package linear

// Issue fields included in all issue queries.
// Expanded inline in each query below for explicitness.

const queryAssignedIssues = `
query AssignedIssues($teamId: String!, $assigneeId: String!, $first: Int, $after: String, $search: String, $labelNames: [String!], $stateTypes: [String!], $stateNames: [String!]) {
	issues(first: $first, after: $after, filter: {
		team: { id: { eq: $teamId } }
		assignee: { id: { eq: $assigneeId } }
		title: { containsIgnoreCase: $search }
		labels: { name: { in: $labelNames } }
		state: {
			type: { in: $stateTypes }
			name: { in: $stateNames }
		}
	}) {
		nodes {
			id identifier title description priority url
			state { id name type }
			labels { nodes { name } }
			assignee { id name }
			creator { id name }
			team { id key }
			createdAt updatedAt
		}
		pageInfo { hasNextPage endCursor }
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
query TeamIssues($teamId: String!, $search: String, $assigneeId: String, $creatorId: String, $subscriberId: String, $labelNames: [String!], $stateTypes: [String!], $stateNames: [String!], $first: Int, $after: String) {
	issues(first: $first, after: $after, filter: {
		team: { id: { eq: $teamId } }
		title: { containsIgnoreCase: $search }
		assignee: { id: { eq: $assigneeId } }
		creator: { id: { eq: $creatorId } }
		subscribers: { id: { eq: $subscriberId } }
		labels: { name: { in: $labelNames } }
		state: {
			type: { in: $stateTypes }
			name: { in: $stateNames }
		}
	}) {
		nodes {
			id identifier title description priority url
			state { id name type }
			labels { nodes { name } }
			assignee { id name }
			creator { id name }
			team { id key }
			createdAt updatedAt
		}
		pageInfo { hasNextPage endCursor }
	}
}`

const queryProjects = `
query Projects($teamId: String!, $search: String, $states: [String!], $first: Int, $after: String) {
	projects(first: $first, after: $after, filter: {
		accessibleTeams: { id: { eq: $teamId } }
		name: { containsIgnoreCase: $search }
		state: { in: $states }
	}) {
		nodes {
			id name description state icon color
			issues {
				nodes {
					id identifier title description
					state { id name type }
					labels { nodes { name } }
					team { id key }
					createdAt updatedAt
				}
			}
		}
		pageInfo { hasNextPage endCursor }
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
query Initiatives($search: String, $statuses: [String!], $first: Int, $after: String) {
	initiatives(first: $first, after: $after, filter: {
		name: { containsIgnoreCase: $search }
		status: { in: $statuses }
	}) {
		nodes {
			id name description status
			projects {
				nodes {
					id name description state
					issues {
						nodes {
							id identifier title description
							state { id name type }
							labels { nodes { name } }
							team { id key }
							createdAt updatedAt
						}
					}
				}
			}
		}
		pageInfo { hasNextPage endCursor }
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
