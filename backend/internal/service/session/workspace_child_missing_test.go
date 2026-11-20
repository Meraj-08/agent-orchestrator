package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A child repository moved after the session started leaves the session's
// child worktree pointing at a gitdir that no longer exists. The Files panel
// must still list the root instead of failing with PROJECT_FOLDER_MISSING.
func TestWorkspaceFilesSkipChildRepoMissingFromDisk(t *testing.T) {
	root := newWorkspaceRepo(t)
	rootBase := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	writeWorkspaceFile(t, root, "root.txt", "changed\n")

	source := filepath.Join(t.TempDir(), "repo-b")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "init")
	runGit(t, source, "config", "user.email", "ao@example.com")
	runGit(t, source, "config", "user.name", "AO Tests")
	writeWorkspaceFile(t, source, "b.go", "package b\n")
	runGit(t, source, "add", ".")
	runGit(t, source, "commit", "-m", "initial child")
	childBase := strings.TrimSpace(runGit(t, source, "rev-parse", "HEAD"))
	child := filepath.Join(root, "repo-b")
	runGit(t, source, "worktree", "add", "-b", "ao/work", child)
	if err := os.Rename(source, source+"-moved"); err != nil {
		t.Fatal(err)
	}

	st := newFakeStore()
	st.projects["ws"] = domain.ProjectRecord{ID: "ws", Kind: domain.ProjectKindWorkspace}
	st.sessions["ws-1"] = domain.SessionRecord{ID: "ws-1", ProjectID: "ws", Metadata: domain.SessionMetadata{WorkspacePath: root}}
	st.worktrees["ws-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "ws-1", RepoName: domain.RootWorkspaceRepoName, WorktreePath: root, BaseSHA: rootBase},
		{SessionID: "ws-1", RepoName: "repo-b", WorktreePath: child, BaseSHA: childBase},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("ListWorkspaceFiles = %v, want the root listed", err)
	}
	if files.Degraded || files.Sections.Uncommitted == nil {
		t.Fatalf("degraded = %v (%s), uncommitted = %v; want complete sections for the remaining repos", files.Degraded, files.DegradedCode, files.Sections.Uncommitted)
	}
	found := false
	for _, f := range files.Files {
		if strings.HasPrefix(f.Path, "repo-b/") {
			t.Fatalf("listed %q from the missing child", f.Path)
		}
		found = found || f.Path == "root.txt"
	}
	if !found {
		t.Fatalf("files = %#v, want root.txt", files.Files)
	}

	manifest, err := (&Service{store: st}).computeWorkspaceManifest(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("workspace manifest = %v, want the root listed", err)
	}
	if manifest.Degraded {
		t.Fatalf("manifest degraded = %v (%s), want complete sections for the remaining repos", manifest.Degraded, manifest.DegradedCode)
	}
	if manifest.WorkspaceVersion != files.WorkspaceVersion {
		t.Fatalf("manifest version %q != files version %q", manifest.WorkspaceVersion, files.WorkspaceVersion)
	}
}

// The root itself being gone is still a whole-workspace failure.
func TestWorkspaceFilesRootRepoMissingStillFails(t *testing.T) {
	source := newWorkspaceRepo(t)
	base := strings.TrimSpace(runGit(t, source, "rev-parse", "HEAD"))
	root := filepath.Join(t.TempDir(), "root-wt")
	runGit(t, source, "worktree", "add", "-b", "ao/work", root)
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	st := newFakeStore()
	st.projects["ws"] = domain.ProjectRecord{ID: "ws", Kind: domain.ProjectKindWorkspace}
	st.sessions["ws-1"] = domain.SessionRecord{ID: "ws-1", ProjectID: "ws", Metadata: domain.SessionMetadata{WorkspacePath: root}}
	st.worktrees["ws-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "ws-1", RepoName: domain.RootWorkspaceRepoName, WorktreePath: root, BaseSHA: base},
	}
	if _, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "ws-1"); !errors.Is(err, ports.ErrWorkspaceRepoUnavailable) {
		t.Fatalf("ListWorkspaceFiles = %v, want ErrWorkspaceRepoUnavailable", err)
	}
}
