package retime

import (
	"fmt"
	"strings"

	"git-retime/internal/git"
)

type Edit struct {
	Author, Committer git.Date
}

func identifiers(commits []*git.Commit) []string {
	ids := make([]string, len(commits))
	for i, c := range commits {
		n := 12
		if n > len(c.OID) {
			n = len(c.OID)
		}
		for j, other := range commits {
			if i == j {
				continue
			}
			for n < len(c.OID) && strings.HasPrefix(other.OID, c.OID[:n]) {
				n++
			}
		}
		ids[i] = c.OID[:n]
	}
	return ids
}

func Document(commits []*git.Commit) []byte {
	var b strings.Builder
	b.WriteString("# Edit dates only; keep rows, hashes and subjects unchanged.\n# Dates require seconds and a timezone. Oldest first, first-parent only.\n# Unchanged files do nothing. Intermediate saves remain on disk.\n# To cancel even after saving, save a '# abort' line or exit the editor with an error.\n# AUTHOR_DATE              COMMITTER_DATE           HASH          SUBJECT\n")
	ids := identifiers(commits)
	for i, c := range commits {
		fmt.Fprintf(&b, "%s  %s  %s  %s\n", c.Author, c.Committer, ids[i], c.Subject)
	}
	return []byte(b.String())
}

func ParseDocument(document []byte, commits []*git.Commit) ([]Edit, bool, error) {
	text := strings.ReplaceAll(string(document), "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return nil, true, nil
	}
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "# abort" {
			return nil, true, nil
		}
	}
	ids := identifiers(commits)
	edits := make([]Edit, 0, len(commits))
	for number, line := range lines {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
			continue
		}
		fail := func(reason string) ([]Edit, bool, error) {
			return nil, false, fmt.Errorf("line %d: %s", number+1, reason)
		}
		if len(edits) >= len(commits) {
			return fail("unexpected extra row")
		}
		var fields [3]string
		for i := range fields {
			line = strings.TrimLeft(line, " \t")
			end := strings.IndexAny(line, " \t")
			if end == -1 {
				fields[i], line = line, ""
			} else {
				fields[i], line = line[:end], line[end:]
			}
		}
		// The two spaces separating hash and subject are structural; preserve
		// additional leading/trailing spaces in the original subject.
		subject := strings.TrimPrefix(line, "  ")
		if fields[2] != ids[len(edits)] || subject != commits[len(edits)].Subject {
			return fail("hash, subject or row order changed")
		}
		author, err := git.ParseDate(fields[0])
		if err != nil {
			return fail("author date: " + err.Error())
		}
		committer, err := git.ParseDate(fields[1])
		if err != nil {
			return fail("committer date: " + err.Error())
		}
		edits = append(edits, Edit{author, committer})
	}
	if len(edits) != len(commits) {
		return nil, false, fmt.Errorf("missing rows: expected %d, found %d", len(commits), len(edits))
	}
	return edits, false, nil
}
