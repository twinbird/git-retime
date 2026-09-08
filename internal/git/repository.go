package git

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Repository struct {
	Dir, Branch, Head string
	Context           context.Context
}

func (r *Repository) command(args ...string) *exec.Cmd {
	cmd := exec.CommandContext(r.Context, "git", args...)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_NO_REPLACE_OBJECTS=1", "GIT_OPTIONAL_LOCKS=0")
	// Let Git clean up its lockfiles on cancellation before forcing termination.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func (r *Repository) Run(input []byte, args ...string) ([]byte, error) {
	cmd := r.command(args...)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func (r *Repository) value(args ...string) (string, error) {
	out, err := r.Run(nil, args...)
	return strings.TrimSpace(string(out)), err
}

func Open(ctx context.Context, dir string) (*Repository, error) {
	r := &Repository{Context: ctx, Dir: dir}
	version, err := r.value("--version")
	if err != nil {
		return nil, err
	}
	v := regexp.MustCompile(`git version (\d+)\.(\d+)`).FindStringSubmatch(version)
	if len(v) != 3 {
		return nil, fmt.Errorf("cannot determine Git version: %s", version)
	}
	major, _ := strconv.Atoi(v[1])
	minor, _ := strconv.Atoi(v[2])
	if major < 2 || (major == 2 && minor < 48) {
		return nil, fmt.Errorf("Git 2.48 or newer is required (found %s)", version)
	}
	if _, err = r.value("rev-parse", "--git-dir"); err != nil {
		return nil, err
	}
	if os.Getenv("GIT_NAMESPACE") != "" {
		return nil, fmt.Errorf("GIT_NAMESPACE is not supported")
	}
	r.Branch, err = r.value("symbolic-ref", "--quiet", "--no-recurse", "HEAD")
	if err != nil || !strings.HasPrefix(r.Branch, "refs/heads/") {
		return nil, fmt.Errorf("HEAD must point to a local branch (detached HEAD is not supported)")
	}
	if err = r.checkBranch(); err != nil {
		return nil, err
	}
	r.Head, err = r.value("rev-parse", "--verify", r.Branch+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("the current branch has no readable commit: %w", err)
	}
	if err = r.CheckState(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Repository) checkBranch() error {
	cmd := r.command("symbolic-ref", "--quiet", r.Branch)
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && e.ExitCode() == 1 {
		return nil // An ordinary direct branch ref.
	}
	return fmt.Errorf("branch %s must be a direct ref", r.Branch)
}

func (r *Repository) CheckState() error {
	shallow, err := r.value("rev-parse", "--is-shallow-repository")
	if err != nil {
		return err
	}
	if shallow == "true" {
		return fmt.Errorf("shallow repositories are not supported")
	}
	base := os.Getenv("GIT_REPLACE_REF_BASE")
	if base == "" {
		base = "refs/replace/"
	}
	replacements, err := r.value("for-each-ref", "--format=%(refname)", base)
	if err != nil {
		return err
	}
	if replacements != "" {
		return fmt.Errorf("replace refs are not supported")
	}
	for _, name := range []string{"MERGE_HEAD", "rebase-merge", "rebase-apply", "CHERRY_PICK_HEAD", "REVERT_HEAD", "sequencer", "info/grafts"} {
		path, err := r.value("rev-parse", "--git-path", name)
		if err != nil {
			return err
		}
		if name == "info/grafts" && os.Getenv("GIT_GRAFT_FILE") != "" {
			path = os.Getenv("GIT_GRAFT_FILE")
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(r.Dir, path)
		}
		info, err := os.Stat(path)
		if err == nil && (name != "info/grafts" || info.Size() > 0) {
			return fmt.Errorf("unsupported repository state: %s exists", name)
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (r *Repository) Commits(count int) ([]*Commit, error) {
	out, err := r.value("rev-list", "--first-parent", "--max-count="+strconv.Itoa(count), r.Head, "--")
	if err != nil {
		return nil, err
	}
	oids := strings.Fields(out)
	commits := make([]*Commit, 0, len(oids))
	for i := len(oids) - 1; i >= 0; i-- {
		raw, err := r.Run(nil, "cat-file", "commit", oids[i])
		if err != nil {
			return nil, err
		}
		commit, err := ParseCommit(oids[i], raw)
		if err != nil {
			return nil, err
		}
		commits = append(commits, commit)
	}
	return commits, nil
}

func (r *Repository) WriteCommit(raw []byte) (string, error) {
	out, err := r.Run(raw, "hash-object", "-t", "commit", "-w", "--stdin")
	return strings.TrimSpace(string(out)), err
}

// Apply prepares the transaction through HEAD, which locks HEAD and its referent.
// Only after checking the symbolic target under that lock do we send commit.
// A separate symref-verify HEAD would conflict with Git's implicit HEAD update.
func (r *Repository) Apply(newHead string) (string, error) {
	oldTree, err := r.value("rev-parse", r.Head+"^{tree}")
	if err != nil {
		return "", err
	}
	newTree, err := r.value("rev-parse", newHead+"^{tree}")
	if err != nil {
		return "", err
	}
	if oldTree != newTree {
		return "", fmt.Errorf("refusing to change the HEAD tree")
	}
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	backup := fmt.Sprintf("refs/retime/backups/%s-%x", time.Now().UTC().Format("20060102T150405Z"), id)
	cmd := r.command("update-ref", "--stdin", "-m", "git-retime: edit commit dates")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return "", err
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		return "", err
	}
	waited := false
	defer func() {
		in.Close() // EOF aborts an uncommitted transaction and releases locks.
		if !waited {
			cmd.Wait()
		}
	}()
	scanner := bufio.NewScanner(out)
	exchange := func(command, expected string) error {
		if _, err := fmt.Fprintln(in, command); err != nil {
			return err
		}
		if !scanner.Scan() || scanner.Text() != expected {
			return fmt.Errorf("Git did not acknowledge %s", expected)
		}
		return nil
	}
	fail := func(cause error) (string, error) {
		in.Close()
		cmd.Wait()
		waited = true
		return "", fmt.Errorf("reference update aborted: %w; %s", cause, strings.TrimSpace(stderr.String()))
	}
	if err := exchange("start", "start: ok"); err != nil {
		return fail(err)
	}
	prepare := fmt.Sprintf("update HEAD %s %s\ncreate %s %s\nprepare", newHead, r.Head, backup, r.Head)
	if err := exchange(prepare, "prepare: ok"); err != nil {
		return fail(err)
	}
	branch, err := r.value("symbolic-ref", "--quiet", "--no-recurse", "HEAD")
	if err != nil || branch != r.Branch {
		return fail(fmt.Errorf("HEAD changed branches while editing"))
	}
	if err := r.checkBranch(); err != nil {
		return fail(err)
	}
	if err := r.CheckState(); err != nil {
		return fail(err)
	}
	if err := exchange("commit", "commit: ok"); err != nil {
		in.Close()
		cmd.Wait()
		waited = true
		return "", fmt.Errorf("commit acknowledgement failed; update may have been applied: %w; inspect HEAD and %s (old %s, new %s): %s", err, backup, r.Head, newHead, strings.TrimSpace(stderr.String()))
	}
	in.Close()
	err = cmd.Wait()
	waited = true
	if err != nil {
		return "", fmt.Errorf("update was committed, but Git exited with an error; backup %s: %w", backup, err)
	}
	return backup, nil
}
