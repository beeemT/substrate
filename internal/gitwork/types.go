package gitwork

import "time"

// Worktree represents a git worktree entry.
type Worktree struct {
	// Path is the absolute path to the worktree directory.
	Path string
	// Branch is the branch name (without refs/heads/ prefix).
	Branch string
	// IsMain indicates whether this is the main worktree.
	IsMain bool
}

// WorkspaceFile represents the content of .substrate-workspace file.
type WorkspaceFile struct {
	// ID is a ULID identifying the workspace.
	ID string `yaml:"id"`
	// Name is a human-readable workspace name.
	Name string `yaml:"name"`
	// CreatedAt is the timestamp when the workspace was created.
	CreatedAt time.Time `yaml:"created_at"`
}
