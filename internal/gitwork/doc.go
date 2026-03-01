// Package gitwork wraps the git-work CLI for worktree management.
//
// This package provides a Go client for the git-work CLI tool, enabling
// programmatic management of git worktrees. It handles workspace discovery,
// worktree creation/removal, and parsing of git-work's JSON output.
//
// # git-work CLI Requirements
//
// This package requires git-work to be installed and available in PATH.
// The git-work CLI must support:
//
//   - list --format=json: Output worktree information as JSON to stderr
//   - checkout -b <branch>: Create a new worktree for the branch (path to stdout)
//   - rm --yes <branch>: Remove a worktree without confirmation
//
// # JSON Format
//
// The git-work list --format=json command outputs JSON to stderr:
//
//	{
//	  "data": {
//	    "worktrees": [
//	      {"dir": "main", "branch": "main", "current": false}
//	    ]
//	  },
//	  "messages": [
//	    {"level": "info", "text": "  main  main\n"}
//	  ]
//	}
//
// The "dir" field contains the directory name (not the full path). This package
// constructs the full path using filepath.Join(repoDir, dir). If git-work is
// updated to return absolute paths, the code handles that gracefully.
//
// # Workspace Discovery
//
// A Substrate workspace is a directory containing a .substrate-workspace file
// (YAML with ULID, name, timestamp). Workspaces contain git-work repositories,
// identified by the presence of a .bare/ subdirectory.
//
// # Example Usage
//
//	client := gitwork.NewClient("")
//
//	// List worktrees
//	worktrees, err := client.List(ctx, repoDir)
//	if err != nil {
//		// handle error
//	}
//
//	// Create a new worktree
//	path, err := client.Checkout(ctx, repoDir, "feature-branch")
//	if err != nil {
//		// handle error
//	}
//
//	// Remove a worktree
//	err = client.Remove(ctx, repoDir, "feature-branch")
//	if err != nil {
//		// handle error
//	}
package gitwork
