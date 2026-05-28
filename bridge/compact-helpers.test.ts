import { describe, expect, test } from "bun:test";
import {
  type CompactableBranchEntry,
  isBenignCompactSkip,
  isCompactionRedundant,
} from "./compact-helpers";

const entry = (type: string): CompactableBranchEntry => ({ type });

describe("isCompactionRedundant", () => {
  test("returns true for an empty branch", () => {
    expect(isCompactionRedundant([])).toBe(true);
  });

  test("returns true when the last entry is a compaction", () => {
    expect(
      isCompactionRedundant([entry("session_init"), entry("message"), entry("compaction")]),
    ).toBe(true);
  });

  test("returns false when the last entry is a non-compaction message", () => {
    expect(
      isCompactionRedundant([entry("session_init"), entry("compaction"), entry("message")]),
    ).toBe(false);
  });

  test("returns false for a branch with only a session_init entry", () => {
    // Non-empty + last entry is not 'compaction', so we let the SDK decide.
    // It will likely throw "Nothing to compact (session too small)", which the
    // catch-side classifier handles separately.
    expect(isCompactionRedundant([entry("session_init")])).toBe(false);
  });
});

describe("isBenignCompactSkip", () => {
  test('matches the literal "Already compacted"', () => {
    expect(isBenignCompactSkip("Already compacted")).toBe(true);
  });

  test('matches "Nothing to compact (session too small)"', () => {
    expect(isBenignCompactSkip("Nothing to compact (session too small)")).toBe(true);
  });

  test('matches any message starting with "Nothing to compact"', () => {
    expect(isBenignCompactSkip("Nothing to compact")).toBe(true);
    expect(isBenignCompactSkip("Nothing to compact: future variation")).toBe(true);
  });

  test("does not match unrelated error messages", () => {
    expect(isBenignCompactSkip("")).toBe(false);
    expect(isBenignCompactSkip("Compaction cancelled")).toBe(false);
    expect(isBenignCompactSkip("No model selected")).toBe(false);
    expect(isBenignCompactSkip("already compacted")).toBe(false); // case-sensitive on purpose
  });
});
