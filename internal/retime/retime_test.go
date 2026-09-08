package retime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"git-retime/internal/git"
)

type fixture struct {
	t         *testing.T
	dir, tree string
}

func newFixture(t *testing.T, format string) *fixture {
	t.Helper()
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_NAMESPACE", "GIT_REPLACE_REF_BASE", "GIT_GRAFT_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_SHALLOW_FILE"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	f := &fixture{t: t, dir: t.TempDir()}
	f.git(nil, "init", "-q", "-b", "main", "--object-format="+format)
	f.git(nil, "config", "user.name", "Test")
	f.git(nil, "config", "user.email", "test@example.com")
	f.git(nil, "config", "commit.gpgsign", "false")
	f.tree = f.git(nil, "mktree")
	return f
}

func (f *fixture) git(input []byte, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = f.dir
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) commit(subject, headers string, parents ...string) string {
	f.t.Helper()
	raw := "tree " + f.tree + "\n"
	for _, p := range parents {
		raw += "parent " + p + "\n"
	}
	raw += "author Original Author <author@example.com> 1577934245 +0000\ncommitter Original Committer <committer@example.com> 1577934306 +0000\n" + headers + "\n" + subject
	return f.git([]byte(raw), "hash-object", "-t", "commit", "-w", "--stdin")
}

func (f *fixture) head(oid string) { f.git(nil, "update-ref", "HEAD", oid) }

func (f *fixture) run(script string, count int) (string, error) {
	f.t.Helper()
	path := filepath.Join(f.t.TempDir(), "test editor.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+script+"\n"), 0700); err != nil {
		f.t.Fatal(err)
	}
	f.t.Setenv("GIT_EDITOR", shellQuote(path))
	var out bytes.Buffer
	err := Run(context.Background(), f.dir, count, strings.NewReader(""), &out, &out)
	return out.String(), err
}

func editScript(expression string) string {
	return "sed " + shellQuote(expression) + " \"$1\" > \"$1.tmp\"\nmv \"$1.tmp\" \"$1\""
}

func (f *fixture) read(oid string) *git.Commit {
	f.t.Helper()
	cmd := exec.Command("git", "cat-file", "commit", oid)
	cmd.Dir = f.dir
	raw, err := cmd.Output()
	if err != nil {
		f.t.Fatal(err)
	}
	c, err := git.ParseCommit(oid, raw)
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

func TestRewriteAndUndo(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			f := newFixture(t, format)
			root := f.commit("root\n\nbody without final newline", "x-custom keep\n continuation\n")
			middle := f.commit("middle", "", root)
			old := f.commit("tip", "", middle)
			f.head(old)
			f.git(nil, "tag", "before", old)
			file := filepath.Join(f.dir, "file")
			os.WriteFile(file, []byte("staged"), 0600)
			f.git(nil, "add", "file")
			os.WriteFile(file, []byte("unstaged"), 0600)
			os.WriteFile(filepath.Join(f.dir, "untracked"), []byte("keep"), 0600)
			indexBefore, _ := os.ReadFile(filepath.Join(f.dir, ".git", "index"))
			statusBefore := f.git(nil, "status", "--porcelain")
			out, err := f.run(editScript("/root/s/2020-01-02T03:04:05Z/2021-01-02T03:04:05Z/"), 10)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "3 commits: 1 date edits, 2 parent-only changes") {
				t.Fatal(out)
			}
			newHead := f.git(nil, "rev-parse", "HEAD")
			if newHead == old {
				t.Fatal("HEAD unchanged")
			}
			repo, err := git.Open(context.Background(), f.dir)
			if err != nil {
				t.Fatal(err)
			}
			commits, err := repo.Commits(10)
			if err != nil {
				t.Fatal(err)
			}
			for i, oid := range []string{root, middle, old} {
				before := f.read(oid)
				mapping := map[string]string{}
				if i > 0 {
					mapping[before.Parents[0]] = commits[i-1].OID
				}
				author := before.Author
				if i == 0 {
					author, _ = git.ParseDate("2021-01-02T03:04:05Z")
				}
				if !bytes.Equal(before.Rewrite(author, before.Committer, mapping), commits[i].Raw) {
					t.Fatal("metadata mismatch")
				}
			}
			if f.git(nil, "rev-parse", "before") != old {
				t.Fatal("tag moved")
			}
			indexAfter, _ := os.ReadFile(filepath.Join(f.dir, ".git", "index"))
			if !bytes.Equal(indexBefore, indexAfter) {
				t.Fatal("index changed")
			}
			if f.git(nil, "status", "--porcelain") != statusBefore {
				t.Fatal("working tree changed")
			}
			if data, _ := os.ReadFile(file); string(data) != "unstaged" {
				t.Fatal("file changed")
			}
			if got := f.git(nil, "for-each-ref", "--format=%(objectname)", "refs/retime/backups/"); got != old {
				t.Fatalf("backup: %s", got)
			}
			f.git(nil, "fsck", "--no-dangling")
			f.git(nil, "update-ref", "refs/heads/main", old, newHead)
			if f.git(nil, "rev-parse", "HEAD") != old {
				t.Fatal("undo failed")
			}
		})
	}
}

func TestNoUpdate(t *testing.T) {
	cases := []struct {
		name, script string
		wantError    bool
	}{
		{"unchanged", ":", false},
		{"abort-after-save", editScript("s/2020-/2021-/g") + "\nprintf '\n# abort\n' >> \"$1\"", false},
		{"empty", "> \"$1\"", false},
		{"editor-error", editScript("s/2020-/2021-/g") + "\nexit 7", true},
		{"invalid-date", editScript("s/2020-01-02/2020-02-30/g"), true},
		{"subject-edit", editScript("s/root/changed/"), true},
		{"missing-row", editScript("/root/d"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "sha1")
			old := f.commit("root", "")
			f.head(old)
			_, err := f.run(tc.script, 10)
			if (err != nil) != tc.wantError {
				t.Fatalf("error: %v", err)
			}
			if f.git(nil, "rev-parse", "HEAD") != old {
				t.Fatal("HEAD moved")
			}
			if f.git(nil, "for-each-ref", "refs/retime/backups/") != "" {
				t.Fatal("backup created")
			}
		})
	}
}

func TestMerge(t *testing.T) {
	f := newFixture(t, "sha1")
	root := f.commit("root", "")
	side := f.commit("side", "", root)
	main := f.commit("main", "", root)
	merge := f.commit("merge", "", main, side)
	f.head(merge)
	f.git(nil, "branch", "side", side)
	_, err := f.run(editScript("/root/s/2020-/2021-/g"), 10)
	if err != nil {
		t.Fatal(err)
	}
	newMerge := f.read("HEAD")
	if len(newMerge.Parents) != 2 || newMerge.Parents[1] != side || newMerge.Parents[0] == main {
		t.Fatal("incorrect merge parents")
	}
	f.git(nil, "merge-base", "--is-ancestor", root, "HEAD")
	if f.git(nil, "rev-parse", "side") != side {
		t.Fatal("side branch moved")
	}
	f.git(nil, "fsck", "--no-dangling")
}

func TestUnsupportedHeaders(t *testing.T) {
	for _, header := range []string{"gpgsig", "gpgsig-sha256", "mergetag"} {
		t.Run(header, func(t *testing.T) {
			f := newFixture(t, "sha1")
			root := f.commit("root", "")
			signed := f.commit("signed", header+" placeholder\n continuation\n", root)
			tip := f.commit("tip", "", signed)
			f.head(tip)
			_, err := f.run(editScript("/root/s/2020-/2021-/g"), 10)
			if err == nil || !strings.Contains(err.Error(), header) {
				t.Fatalf("expected header rejection: %v", err)
			}
			if f.git(nil, "rev-parse", "HEAD") != tip {
				t.Fatal("HEAD moved")
			}
			_, err = f.run(editScript("/tip/s/2020-/2021-/g"), 10)
			if err != nil {
				t.Fatal(err)
			}
			if f.read("HEAD").Parents[0] != signed {
				t.Fatal("unchanged signed ancestor rewritten")
			}
		})
	}
}

func TestConcurrentChanges(t *testing.T) {
	for _, kind := range []string{"advance", "switch-same-oid", "detach", "lock"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t, "sha1")
			old := f.commit("root", "")
			other := f.commit("other", "", old)
			f.head(old)
			f.git(nil, "branch", "other", old)
			mutation := map[string]string{
				"advance":         "git update-ref HEAD " + other,
				"switch-same-oid": "git symbolic-ref HEAD refs/heads/other",
				"detach":          "git update-ref --no-deref HEAD " + old,
				"lock":            "touch .git/refs/heads/main.lock",
			}[kind]
			_, err := f.run(editScript("s/2020-/2021-/g")+"\n"+mutation, 1)
			if err == nil {
				t.Fatal("accepted concurrent mutation")
			}
			want := old
			if kind == "advance" {
				want = other
			}
			if f.git(nil, "rev-parse", "refs/heads/main") != want {
				t.Fatal("overwrote concurrent change")
			}
			if f.git(nil, "rev-parse", "refs/heads/other") != old {
				t.Fatal("other branch moved")
			}
			if f.git(nil, "for-each-ref", "refs/retime/backups/") != "" {
				t.Fatal("partial backup")
			}
		})
	}
}

func TestDateColumnsAndCount(t *testing.T) {
	for _, tc := range []struct{ name, expression, author, committer string }{
		{"author", "s/2020-01-02T03:04:05Z/2021-01-02T03:04:05Z/", "2021-01-02T03:04:05Z", "2020-01-02T03:05:06Z"},
		{"committer", "s/2020-01-02T03:05:06Z/2021-01-02T03:05:06Z/", "2020-01-02T03:04:05Z", "2021-01-02T03:05:06Z"},
		{"offset", "s/2020-01-02T03:04:05Z/2020-01-02T12:04:05+09:00/", "2020-01-02T12:04:05+09:00", "2020-01-02T03:05:06Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "sha1")
			root := f.commit("root", "")
			f.head(f.commit("tip", "", root))
			_, err := f.run(editScript(tc.expression), 1)
			if err != nil {
				t.Fatal(err)
			}
			c := f.read("HEAD")
			if c.Author.String() != tc.author || c.Committer.String() != tc.committer || c.Parents[0] != root {
				t.Fatalf("unexpected result: %+v", c)
			}
		})
	}
}

func TestLinkedWorktree(t *testing.T) {
	f := newFixture(t, "sha1")
	old := f.commit("root", "")
	f.head(old)
	path := filepath.Join(t.TempDir(), "linked tree")
	f.git(nil, "worktree", "add", "-q", "-b", "linked", path)
	linked := &fixture{t: t, dir: path, tree: f.tree}
	_, err := linked.run(editScript("s/2020-/2021-/g"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if f.git(nil, "rev-parse", "main") != old || linked.git(nil, "rev-parse", "HEAD") == old {
		t.Fatal("wrong branch updated")
	}
}

func TestRejectedStates(t *testing.T) {
	for _, state := range []string{"empty", "detached", "shallow", "replace", "info/grafts", "MERGE_HEAD", "rebase-merge", "rebase-apply", "CHERRY_PICK_HEAD", "REVERT_HEAD", "sequencer"} {
		t.Run(state, func(t *testing.T) {
			f := newFixture(t, "sha1")
			if state != "empty" {
				old := f.commit("root", "")
				f.head(old)
				switch state {
				case "detached":
					f.git(nil, "update-ref", "--no-deref", "HEAD", old)
				case "shallow":
					os.WriteFile(filepath.Join(f.dir, ".git", "shallow"), []byte(old+"\n"), 0600)
				case "replace":
					f.git(nil, "update-ref", "refs/replace/"+old, f.commit("replacement", ""))
				default:
					os.WriteFile(filepath.Join(f.dir, ".git", state), []byte(old+"\n"), 0600)
				}
			}
			_, err := f.run("exit 42", 1)
			if err == nil || strings.Contains(err.Error(), "editor failed") {
				t.Fatalf("state not rejected before editor: %v", err)
			}
		})
	}
}

func TestUndoRejectsAdvancedBranch(t *testing.T) {
	f := newFixture(t, "sha1")
	old := f.commit("root", "")
	f.head(old)
	_, err := f.run(editScript("s/2020-/2021-/g"), 1)
	if err != nil {
		t.Fatal(err)
	}
	newHead := f.git(nil, "rev-parse", "HEAD")
	advanced := f.commit("advanced", "", newHead)
	f.head(advanced)
	cmd := exec.Command("git", "update-ref", "refs/heads/main", old, newHead)
	cmd.Dir = f.dir
	if err := cmd.Run(); err == nil {
		t.Fatal("stale undo succeeded")
	}
	if f.git(nil, "rev-parse", "HEAD") != advanced {
		t.Fatal("advanced commit lost")
	}
}

func TestRootMessages(t *testing.T) {
	for i, body := range []string{"", " \t\x1b\xff hi  \nbody", strings.Repeat("long", 20000)} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			f := newFixture(t, "sha1")
			old := f.commit(body, "encoding ISO-8859-1\n")
			f.head(old)
			_, err := f.run(editScript("s/2020-/2021-/g"), 1)
			if err != nil {
				t.Fatal(err)
			}
			before, after := f.read(old), f.read("HEAD")
			if !bytes.Equal(before.Rewrite(after.Author, after.Committer, nil), after.Raw) {
				t.Fatal("message changed")
			}
		})
	}
}
