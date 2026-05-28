package views

import (
	"github.com/beeemT/substrate/internal/domain"
)

// leafAgentSessions returns the leaf nodes of the agent-session graph defined
// by domain.AgentSession.ParentAgentSessionID. A leaf is a session that no
// other session in the input slice points to as its parent.
//
// Algorithm:
//  1. Build hasChild[parentID] = true for every non-empty ParentAgentSessionID.
//  2. A session is a tentative leaf when its ID is not in hasChild.
//  3. For legacy rows with no graph edges at all, fall back to today's
//     transitive approximation: group tentative leaves by
//     (sub_plan_id, repository_name) and keep only the newest one per group,
//     ordered by CreatedAt, then UpdatedAt, then ID. A group is treated as
//     "legacy" only when none of its members participate in any graph edge —
//     no member has a parent and no member is anyone's parent. As soon as
//     any member of the group has a parent or child link, all leaves in the
//     group are kept verbatim because the graph is the authoritative source.
//
// Order of returned leaves is not specified beyond "stable enough for the
// callers" — they all re-sort or re-project as needed.
func leafAgentSessions(sessions []domain.AgentSession) []domain.AgentSession {
	if len(sessions) == 0 {
		return nil
	}

	hasChild := make(map[string]bool, len(sessions))
	for i := range sessions {
		if pid := sessions[i].ParentAgentSessionID; pid != "" {
			hasChild[pid] = true
		}
	}

	type groupKey struct {
		subPlanID      string
		repositoryName string
	}
	type groupInfo struct {
		leaves  []domain.AgentSession
		anyEdge bool
	}

	groups := make(map[groupKey]*groupInfo)
	getGroup := func(k groupKey) *groupInfo {
		g, ok := groups[k]
		if !ok {
			g = &groupInfo{}
			groups[k] = g
		}
		return g
	}

	for i := range sessions {
		s := sessions[i]
		k := groupKey{subPlanID: s.SubPlanID, repositoryName: s.RepositoryName}
		g := getGroup(k)
		// Any session that has a parent or has at least one child contributes
		// a graph edge to its group. Non-leaves still contribute their edge,
		// even though they are not added to the leaves slice.
		if s.ParentAgentSessionID != "" || hasChild[s.ID] {
			g.anyEdge = true
		}
		if hasChild[s.ID] {
			continue
		}
		g.leaves = append(g.leaves, s)
	}

	leaves := make([]domain.AgentSession, 0, len(sessions))
	for _, g := range groups {
		if len(g.leaves) == 0 {
			continue
		}
		if g.anyEdge {
			leaves = append(leaves, g.leaves...)
			continue
		}
		// Legacy fallback: no graph edges in this group. Pick the newest leaf
		// only, mirroring the pre-graph "newest by created/updated/id wins"
		// behavior so old interrupted/failed rows do not poison work-item
		// labels when a newer session for the same repo replaced them.
		latest := g.leaves[0]
		for i := 1; i < len(g.leaves); i++ {
			if leafIsNewer(g.leaves[i], latest) {
				latest = g.leaves[i]
			}
		}
		leaves = append(leaves, latest)
	}
	return leaves
}

// leafIsNewer reports whether a should be treated as newer than b for
// legacy-fallback leaf selection. CreatedAt is the primary key (matches the
// retry/resume flow which always creates a new row with a fresh CreatedAt),
// UpdatedAt and ID break ties.
func leafIsNewer(a, b domain.AgentSession) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.ID > b.ID
}
