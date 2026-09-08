package git

import (
	"bytes"
	"strings"
	"testing"
)

func TestDateValidation(t *testing.T) {
	for _, s := range []string{"2026-09-08T18:00:00+09:00", "2026-09-08T09:00:00Z", "0000-01-01T00:00:00Z", "9999-12-31T23:59:59-23:59"} {
		if _, err := ParseDate(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	for _, s := range []string{"2026-09-08", "2026-09-08T18:00:00", "2026-09-08T18:00:00.1Z", "2026-02-30T18:00:00Z", "2026-09-08T18:00:60Z", "2026-09-08T18:00:00+24:00", "2026-09-08T18:00:00+09:60", "2026-09-08T18:00:00-00:00"} {
		if _, err := ParseDate(s); err == nil {
			t.Errorf("accepted %s", s)
		}
	}
	a, _ := ParseDate("2026-09-08T18:00:00+09:00")
	b, _ := ParseDate("2026-09-08T09:00:00Z")
	if a.Seconds != b.Seconds || a == b {
		t.Fatal("timezone-only edits must differ")
	}
}

func TestRewritePreservesBytes(t *testing.T) {
	for _, body := range []string{"", "subject\n\nbody\n", "subject\nno final newline", " \t\x1b\xffsubject  \n\x00body"} {
		raw := []byte("tree " + strings.Repeat("a", 40) + "\nparent " + strings.Repeat("b", 40) + "\nauthor A <a@e> 1577934245 -0000\ncommitter C <c@e> 1577934306 +0900\nencoding ISO-8859-1\nx-custom value\n continuation\n\n" + body)
		c, err := ParseCommit("test", raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.Rewrite(c.Author, c.Committer, nil); !bytes.Equal(got, raw) {
			t.Fatal("unchanged data was normalized")
		}
		a := c.Author
		a.Seconds++
		got := c.Rewrite(a, c.Committer, map[string]string{strings.Repeat("b", 40): strings.Repeat("c", 40)})
		want := bytes.Replace(raw, []byte("1577934245 -0000"), []byte("1577934246 +0000"), 1)
		want = bytes.Replace(want, []byte("parent "+strings.Repeat("b", 40)), []byte("parent "+strings.Repeat("c", 40)), 1)
		if !bytes.Equal(got, want) {
			t.Fatalf("unexpected rewrite\ngot %q\nwant %q", got, want)
		}
		if !bytes.Equal(c.Raw, raw) {
			t.Fatal("mutated original data")
		}
	}
}
