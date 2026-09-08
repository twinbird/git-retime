package git

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Date compares both the instant and the recorded timezone, unlike time.Time.Equal.
type Date struct {
	Seconds int64
	Offset  int
}

func (d Date) String() string {
	return time.Unix(d.Seconds, 0).In(time.FixedZone("", d.Offset)).Format(time.RFC3339)
}

func (d Date) raw() string {
	sign, offset := "+", d.Offset
	if offset < 0 {
		sign, offset = "-", -offset
	}
	return fmt.Sprintf("%d %s%02d%02d", d.Seconds, sign, offset/3600, offset/60%60)
}

var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})$`)

func ParseDate(s string) (Date, error) {
	if !datePattern.MatchString(s) {
		return Date{}, fmt.Errorf("expected a date with seconds and timezone, e.g. 2026-09-08T18:00:00+09:00")
	}
	if s[len(s)-1] != 'Z' {
		h, _ := strconv.Atoi(s[len(s)-5 : len(s)-3])
		m, _ := strconv.Atoi(s[len(s)-2:])
		if h > 23 || m > 59 || strings.HasSuffix(s, "-00:00") {
			return Date{}, fmt.Errorf("invalid or unknown timezone offset")
		}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return Date{}, err
	}
	_, offset := t.Zone()
	return Date{t.Unix(), offset}, nil
}

type Commit struct {
	OID, Tree         string
	Parents           []string
	Author, Committer Date
	Subject           string
	UnsupportedHeader string
	Raw               []byte
	headers           [][]byte
	body              []byte
}

func parseIdent(line []byte) (Date, error) {
	i := bytes.LastIndexByte(line, ' ')
	if i < 0 {
		return Date{}, fmt.Errorf("invalid identity")
	}
	z := string(line[i+1:])
	j := bytes.LastIndexByte(line[:i], ' ')
	if j < 0 || !bytes.HasSuffix(line[:j], []byte(">")) || len(z) != 5 || (z[0] != '+' && z[0] != '-') {
		return Date{}, fmt.Errorf("invalid identity or timezone")
	}
	seconds, err := strconv.ParseInt(string(line[j+1:i]), 10, 64)
	if err != nil {
		return Date{}, err
	}
	for _, c := range z[1:] {
		if c < '0' || c > '9' {
			return Date{}, fmt.Errorf("invalid timezone")
		}
	}
	h, _ := strconv.Atoi(z[1:3])
	m, _ := strconv.Atoi(z[3:])
	if h > 23 || m > 59 {
		return Date{}, fmt.Errorf("invalid timezone")
	}
	offset := (h*60 + m) * 60
	if z[0] == '-' {
		offset = -offset
	}
	d := Date{seconds, offset}
	// Refuse dates that the edit format cannot round-trip.
	parsed, err := ParseDate(d.String())
	if err != nil || parsed != d {
		return Date{}, fmt.Errorf("date is outside the supported four-digit year range")
	}
	return d, nil
}

func ParseCommit(oid string, raw []byte) (*Commit, error) {
	parts := bytes.SplitN(raw, []byte("\n\n"), 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("commit %s has no header separator", oid)
	}
	c := &Commit{OID: oid, Raw: raw, headers: bytes.Split(parts[0], []byte("\n")), body: parts[1]}
	seen := map[string]int{}
	for _, line := range c.headers {
		if len(line) > 0 && line[0] == ' ' {
			continue
		}
		key, value, ok := bytes.Cut(line, []byte(" "))
		if !ok {
			return nil, fmt.Errorf("commit %s has an invalid header", oid)
		}
		k := string(key)
		seen[k]++
		var err error
		switch k {
		case "tree":
			c.Tree = string(value)
		case "parent":
			c.Parents = append(c.Parents, string(value))
		case "author":
			c.Author, err = parseIdent(value)
		case "committer":
			c.Committer, err = parseIdent(value)
		case "gpgsig", "gpgsig-sha256", "mergetag":
			c.UnsupportedHeader = k
		}
		if err != nil {
			return nil, fmt.Errorf("commit %s %s: %w", oid, k, err)
		}
	}
	for _, k := range []string{"tree", "author", "committer"} {
		if seen[k] != 1 {
			return nil, fmt.Errorf("commit %s must contain exactly one %s header", oid, k)
		}
	}
	subject, _, _ := bytes.Cut(c.body, []byte("\n"))
	// Quote escapes control bytes and invalid UTF-8 without altering the commit.
	c.Subject = strconv.Quote(string(subject))
	c.Subject = c.Subject[1 : len(c.Subject)-1]
	return c, nil
}

func replaceDate(line []byte, d Date) []byte {
	i := bytes.LastIndexByte(line, ' ')
	j := bytes.LastIndexByte(line[:i], ' ')
	return []byte(string(line[:j+1]) + d.raw())
}

// Rewrite retains every byte except edited dates and mapped parent OIDs.
func (c *Commit) Rewrite(author, committer Date, mapping map[string]string) []byte {
	lines := make([][]byte, len(c.headers))
	for i, line := range c.headers {
		lines[i] = line
		switch {
		case bytes.HasPrefix(line, []byte("author ")) && author != c.Author:
			lines[i] = replaceDate(line, author)
		case bytes.HasPrefix(line, []byte("committer ")) && committer != c.Committer:
			lines[i] = replaceDate(line, committer)
		case bytes.HasPrefix(line, []byte("parent ")):
			if oid, ok := mapping[string(line[7:])]; ok {
				lines[i] = []byte("parent " + oid)
			}
		}
	}
	result := append(bytes.Join(lines, []byte("\n")), '\n', '\n')
	return append(result, c.body...)
}
