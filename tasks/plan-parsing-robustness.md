# Plan: Robust Plan Parsing

## Status: ✅ IMPLEMENTED

---

## Problem Statement

The plan parser failed when models produced:
1. Numbered lists instead of bullet lists (`1. Item` vs `- Item`)
2. Lists with inconsistent formatting (mixed bullets/numbers)
3. Lists that look like lists to humans but not to regex (`(1)` or `[x]` style)

The error `backend.events: ### Changes must include at least 3 list item(s)` occurred because the original regex `(?m)^\s*(?:[-*+] |\d+\. )\S` was too strict.

---

## Implementation Summary

### 1. Reduced Minimum List Items (parser.go:225)
Changed from `minListItems: 3` to `minListItems: 1` for the Changes section.

### 2. Enhanced List Format Recognition (parser.go:273-305)
Updated `countMarkdownListItems()` to accept multiple formats:

**Supported patterns:**
- Bullets: `- Item`, `* Item`, `+ Item`
- Numbered: `1. Item`, `1) Item`
- Lettered: `a. Item`, `a) Item`
- Parenthesized: `(1) Item`
- Checkbox: `- [ ] Item`, `- [x] Item`

**Rejected patterns:**
- `1Add feature X` (no space after number)
- `-Add feature X` (no space after dash)
- `Step 1: Add X` (text before marker)

### 3. Updated Planning Templates (planning.go:998-1020)
- Changed "at least three" to "at least one"
- Added "Valid List Formats" section with examples
- Added "Invalid (will be rejected)" section

### 4. Updated Correction Template (planning.go:1040-1044)
- Changed "three concrete steps" to "one concrete step"
- Added list format examples

### 5. Added Comprehensive Tests (parser_test.go)
19 new test cases covering all supported list formats.

---

## Files Changed

| File | Change |
|------|--------|
| `internal/orchestrator/parser.go` | Updated `countMarkdownListItems()`, reduced min list items |
| `internal/orchestrator/planning.go` | Updated planning and correction templates |
| `internal/orchestrator/parser_test.go` | Added 19 test cases |

---

## Test Results

```
$ go test ./internal/orchestrator/... -v
224 passed in 1 packages

$ go test ./internal/... -count=1  
2214 passed in 31 packages
```
