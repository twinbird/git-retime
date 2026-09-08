package retime

import (
	"strings"
	"testing"

	"git-retime/internal/git"
)

func documentCommits() []*git.Commit {
	d, _ := git.ParseDate("2020-01-02T03:04:05Z")
	return []*git.Commit{
		{OID: strings.Repeat("a", 39) + "b", Author: d, Committer: d, Subject: "  root  "},
		{OID: strings.Repeat("a", 39) + "c", Author: d, Committer: d, Subject: ""},
	}
}

func TestDocument(t *testing.T) {
	commits := documentCommits()
	doc := string(Document(commits))
	for _, text := range []string{doc, strings.ReplaceAll(doc, "\n", "\r\n"), doc + "# comment\n\n"} {
		edits, aborted, err := ParseDocument([]byte(text), commits)
		if err != nil || aborted || len(edits) != 2 || edits[0].Author != commits[0].Author {
			t.Fatalf("round trip: %v %v %v", edits, aborted, err)
		}
	}
	for _, text := range []string{"", " \n", doc + "# abort\n"} {
		_, aborted, err := ParseDocument([]byte(text), commits)
		if err != nil || !aborted {
			t.Fatal("expected cancellation")
		}
	}
	lines := strings.Split(strings.TrimSuffix(doc, "\n"), "\n")
	last := len(lines) - 1
	bad := []string{
		strings.Replace(doc, "root", "changed", 1),
		strings.Replace(doc, commits[0].OID, commits[1].OID, 1),
		strings.Join(lines[:last], "\n"),
		doc + lines[last] + "\n",
		strings.Replace(doc, "2020-01-02T03:04:05Z", "bad", 1),
		"# only comments\n",
	}
	lines[last], lines[last-1] = lines[last-1], lines[last]
	bad = append(bad, strings.Join(lines, "\n"))
	for _, text := range bad {
		if _, _, err := ParseDocument([]byte(text), commits); err == nil {
			t.Errorf("accepted invalid document: %q", text)
		}
	}
}

func TestLongSubject(t *testing.T) {
	c := documentCommits()[:1]
	c[0].Subject = strings.Repeat("x", 100000)
	if _, _, err := ParseDocument(Document(c), c); err != nil {
		t.Fatal(err)
	}
}
