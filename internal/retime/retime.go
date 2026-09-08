package retime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"git-retime/internal/editor"
	"git-retime/internal/git"
)

func Run(ctx context.Context, dir string, count int, stdin io.Reader, stdout, stderr io.Writer) error {
	if count <= 0 {
		return fmt.Errorf("count must be positive")
	}
	repo, err := git.Open(ctx, dir)
	if err != nil {
		return err
	}
	commits, err := repo.Commits(count)
	if err != nil {
		return err
	}
	command, err := repo.Run(nil, "var", "GIT_EDITOR")
	if err != nil {
		return err
	}
	document := Document(commits)
	edited, err := editor.Edit(ctx, dir, strings.TrimSpace(string(command)), document, stdin, stdout, stderr)
	if err != nil {
		return err
	}
	edits, aborted, err := ParseDocument(edited, commits)
	if err != nil {
		return err
	}
	if aborted {
		_, err := fmt.Fprintln(stdout, "Cancelled. No changes applied.")
		return err
	}
	// Validate all affected commits before writing any objects.
	affected := make(map[string]bool)
	changedDates := 0
	for i, c := range commits {
		changed := edits[i].Author != c.Author || edits[i].Committer != c.Committer
		if changed {
			changedDates++
		}
		for _, p := range c.Parents {
			changed = changed || affected[p]
		}
		if changed && c.UnsupportedHeader != "" {
			return fmt.Errorf("cannot rewrite %s: %s is not supported in v1", c.OID, c.UnsupportedHeader)
		}
		affected[c.OID] = changed
	}
	if changedDates == 0 {
		_, err := fmt.Fprintln(stdout, "No changes.")
		return err
	}
	mapping := make(map[string]string)
	rewritten := 0
	for i, c := range commits {
		raw := c.Rewrite(edits[i].Author, edits[i].Committer, mapping)
		if bytes.Equal(raw, c.Raw) {
			mapping[c.OID] = c.OID
			continue
		}
		oid, err := repo.WriteCommit(raw)
		if err != nil {
			return err
		}
		mapping[c.OID] = oid
		rewritten++
	}
	newHead := mapping[repo.Head]
	backup, err := repo.Apply(newHead)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Rewrote %d commits: %d date edits, %d parent-only changes.\nold HEAD: %s\nnew HEAD: %s\nbackup: %s\n\nTo undo (only if the branch has not advanced):\n  git update-ref %s %s %s\n", rewritten, changedDates, rewritten-changedDates, repo.Head, newHead, backup, shellQuote(repo.Branch), repo.Head, newHead)
	if err != nil {
		return fmt.Errorf("history was updated, but result output failed (backup %s): %w", backup, err)
	}
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
