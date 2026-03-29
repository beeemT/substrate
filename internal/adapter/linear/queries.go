package linear

import "strings"

type queryVarFilter struct {
	varName string
	decl    string
	filter  string
}

func appendOptionalFilters(vars map[string]any, optional []queryVarFilter, decls, filters *strings.Builder) {
	for _, vf := range optional {
		if v, ok := vars[vf.varName]; ok && v != nil {
			decls.WriteString(", ")
			decls.WriteString(vf.decl)
			filters.WriteString("\n\t\t")
			filters.WriteString(vf.filter)
		}
	}
}

// Issue fields included in all issue queries.
// Expanded inline in each query below for explicitness.

const queryIssuesByIDs = `
query IssuesByIDs($ids: [ID!]!) {
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

const issueFields = `
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
`

// buildIssueQuery constructs a GraphQL issue query with only non-nil filter
// conditions. Linear interprets null comparator values as "field must be null"
// rather than "skip this filter", so we must omit conditions entirely when
// the caller does not intend to filter on them.
func buildIssueQuery(name string, vars map[string]any) string {
	// Variable declarations — always include pagination.
	var decls strings.Builder
	decls.WriteString("$first: Int, $after: String")
	// Filter conditions — only include when the variable is present and non-nil.
	var filters strings.Builder

	optional := []queryVarFilter{
		{"teamId", "$teamId: ID", "team: { id: { eq: $teamId } }"},
		{"assigneeId", "$assigneeId: ID!", "assignee: { id: { eq: $assigneeId } }"},
		{"creatorId", "$creatorId: ID!", "creator: { id: { eq: $creatorId } }"},
		{"subscriberId", "$subscriberId: ID!", "subscribers: { id: { eq: $subscriberId } }"},
		{"search", "$search: String", "title: { containsIgnoreCase: $search }"},
		{"labelNames", "$labelNames: [String!]", "labels: { name: { in: $labelNames } }"},
	}
	appendOptionalFilters(vars, optional, &decls, &filters)

	// State filter is composite (type + name); include the block only if at least
	// one sub-condition is present.
	stateTypes, hasTypes := vars["stateTypes"]
	stateNames, hasNames := vars["stateNames"]
	if (hasTypes && stateTypes != nil) || (hasNames && stateNames != nil) {
		var stateInner strings.Builder
		if hasTypes && stateTypes != nil {
			decls.WriteString(", $stateTypes: [String!]")
			stateInner.WriteString("\n\t\t\ttype: { in: $stateTypes }")
		}
		if hasNames && stateNames != nil {
			decls.WriteString(", $stateNames: [String!]")
			stateInner.WriteString("\n\t\t\tname: { in: $stateNames }")
		}
		filters.WriteString("\n\t\tstate: {")
		filters.WriteString(stateInner.String())
		filters.WriteString("\n\t\t}")
	}

	return "query " + name + "(" + decls.String() + ") {\n\tissues(first: $first, after: $after, filter: {" +
		filters.String() + "\n\t}) {" + issueFields + "\t}\n}"
}

const projectFields = `
		nodes {
			id name description state icon color
			createdAt updatedAt
		}
		pageInfo { hasNextPage endCursor }
`

func buildProjectQuery(vars map[string]any) string {
	var decls strings.Builder
	decls.WriteString("$first: Int, $after: String")
	var filters strings.Builder

	optional := []queryVarFilter{
		{"teamId", "$teamId: ID", "accessibleTeams: { id: { eq: $teamId } }"},
		{"search", "$search: String", "name: { containsIgnoreCase: $search }"},
		{"states", "$states: [String!]", "state: { in: $states }"},
	}
	appendOptionalFilters(vars, optional, &decls, &filters)

	return "query Projects(" + decls.String() + ") {\n\tprojects(first: $first, after: $after, filter: {" +
		filters.String() + "\n\t}) {" + projectFields + "\t}\n}"
}

const queryProjectWithIssues = `
query ProjectWithIssues($id: ID!) {
	project(id: $id) {
		id name description state icon color
		issues(filter: { state: { type: { nin: ["completed", "canceled"] } } }) {
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

const initiativeFields = `
		nodes {
			id name description status
			createdAt updatedAt
			projects {
				nodes {
					id name
				}
			}
		}
		pageInfo { hasNextPage endCursor }
`

func buildInitiativeQuery(vars map[string]any) string {
	var decls strings.Builder
	decls.WriteString("$first: Int, $after: String")
	var filters strings.Builder

	optional := []queryVarFilter{
		{"search", "$search: String", "name: { containsIgnoreCase: $search }"},
		{"statuses", "$statuses: [String!]", "status: { in: $statuses }"},
	}
	appendOptionalFilters(vars, optional, &decls, &filters)

	return "query Initiatives(" + decls.String() + ") {\n\tinitiatives(first: $first, after: $after, filter: {" +
		filters.String() + "\n\t}) {" + initiativeFields + "\t}\n}"
}

// stripNilVars removes nil-valued entries from vars so the GraphQL request
// doesn't include null values for variables that were excluded from the query.
func stripNilVars(vars map[string]any) map[string]any {
	clean := make(map[string]any, len(vars))
	for k, v := range vars {
		if v != nil {
			clean[k] = v
		}
	}
	return clean
}

const querySingleInitiative = `
query SingleInitiative($id: ID!) {
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
mutation UpdateIssueState($issueId: ID!, $stateId: String!) {
	issueUpdate(id: $issueId, input: { stateId: $stateId }) {
		success
	}
}`

const mutationAddComment = `
mutation AddComment($issueId: ID!, $body: String!) {
	commentCreate(input: { issueId: $issueId, body: $body }) {
		success
	}
}`
