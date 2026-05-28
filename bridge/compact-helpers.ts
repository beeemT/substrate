/**
 * compact-helpers.ts — predicates for safely skipping manual context compaction.
 *
 * Oh My Pi's `AgentSession.compact()` calls `#disconnectFromAgent()` and
 * `await this.abort()` BEFORE checking whether the session has anything to
 * compact. When the session was already compacted (e.g. resumed from a session
 * file whose last entry is a compaction entry) the SDK throws after that
 * destructive setup, leaving the session unable to drive a follow-up
 * `prompt()`. The bridge therefore predicts the failure ahead of time and
 * skips the SDK call entirely; the catch-side classifier covers any cases the
 * pre-check misses (e.g. "Nothing to compact (session too small)" which depends
 * on settings we don't replicate here).
 */

/** Minimal structural shape of an entry returned by SessionManager.getBranch(). */
export interface CompactableBranchEntry {
  readonly type: string;
}

/**
 * Returns true if calling `session.compact()` on a session whose branch ends
 * with `branch` would be a no-op or would throw "Already compacted".
 *
 * - Empty branch: nothing to compact.
 * - Last entry is a compaction entry: session is already compacted.
 */
export function isCompactionRedundant(branch: readonly CompactableBranchEntry[]): boolean {
  if (branch.length === 0) return true;
  return branch[branch.length - 1]?.type === "compaction";
}

/**
 * Returns true if the given error message from `session.compact()` indicates
 * a benign skip — i.e. the session is already in (or close enough to) a
 * compacted state and the request can be reported as a successful no-op.
 *
 * Matches the literal strings thrown by Oh My Pi's
 * `pi-coding-agent` SDK from `agent-session.ts#compact`.
 */
export function isBenignCompactSkip(message: string): boolean {
  if (message === "Already compacted") return true;
  if (message.startsWith("Nothing to compact")) return true;
  return false;
}
