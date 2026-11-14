package gitworktree

import "strings"

func checkRefFormatBranchArgs(repo, branch string) []string {
	return []string{"-C", repo, "check-ref-format", "--branch", branch}
}

func revParseVerifyArgs(repo, ref string) []string {
	return []string{"-C", repo, "rev-parse", "--verify", "--quiet", ref}
}

func remoteListArgs(repo string) []string {
	return []string{"-C", repo, "remote"}
}

func fetchBranchArgs(repo, remote, branch string) []string {
	return []string{"-C", repo, "fetch", remote, "+refs/heads/" + branch + ":refs/remotes/" + remote + "/" + branch}
}

const worktreeAddForce = "--force"

func worktreeAddBranchArgs(repo, path, branch string, force bool) []string {
	args := []string{"-C", repo, "worktree", "add"}
	if force {
		args = append(args, worktreeAddForce)
	}
	return append(args, path, branch)
}

func worktreeAddNewBranchArgs(repo, branch, path, baseRef string, force bool) []string {
	args := []string{"-C", repo, "worktree", "add"}
	if force {
		args = append(args, worktreeAddForce)
	}
	return append(args, "-b", branch, path, baseRef)
}

func worktreeRemoveArgs(repo, path string) []string {
	return []string{"-C", repo, "worktree", "remove", path}
}

func worktreeForceRemoveArgs(repo, path string) []string {
	return []string{"-C", repo, "worktree", "remove", "--force", path}
}

func worktreePruneArgs(repo string) []string {
	return []string{"-C", repo, "worktree", "prune"}
}

func statusPorcelainArgs(path string) []string {
	return []string{"-C", path, "status", "--porcelain"}
}

func worktreeListPorcelainArgs(repo string) []string {
	return []string{"-C", repo, "worktree", "list", "--porcelain"}
}

func addAllTempIndexArgs(worktree string) []string {
	return []string{"-C", worktree, "add", "-A"}
}

func writeTreeArgs(worktree string) []string {
	return []string{"-C", worktree, "write-tree"}
}

func commitTreeArgs(worktree, treeSHA, parent, message string) []string {
	args := []string{"-C", worktree, "commit-tree", treeSHA}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	args = append(args, "-m", message)
	return args
}

func updateRefArgs(worktree, ref, commitSHA string) []string {
	return []string{"-C", worktree, "update-ref", ref, commitSHA}
}

func deleteRefArgs(worktree, ref string) []string {
	return []string{"-C", worktree, "update-ref", "-d", ref}
}

func revParseHeadArgs(worktree string) []string {
	return []string{"-C", worktree, "rev-parse", "--verify", "HEAD"}
}

func cherryPickNoCommitArgs(worktree, commitSHA string) []string {
	return []string{"-C", worktree, "cherry-pick", "--no-commit", commitSHA}
}

func ignoredCountArgs(worktree string) []string {
	return []string{"-C", worktree, "status", "--ignored", "--porcelain"}
}

func configuredBaseRefCandidates(defaultBranch string) []string {
	if strings.Contains(defaultBranch, "/") {
		return []string{defaultBranch}
	}
	return []string{"origin/" + defaultBranch, "refs/heads/" + defaultBranch}
}
