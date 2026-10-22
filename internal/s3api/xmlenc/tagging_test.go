package xmlenc

import (
	"testing"

	"github.com/eukalpia/unio/internal/object"
)

func TestAppendTagging(t *testing.T) {
	const decl = `<?xml version="1.0" encoding="UTF-8"?>`
	const root = `<Tagging xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`
	if got := string(AppendTagging(nil, nil)); got != decl+root+`<TagSet></TagSet></Tagging>` {
		t.Fatalf("empty set = %q", got)
	}
	tags := object.TagSet{{Key: "env", Value: "it"}, {Key: "a&b", Value: "<x>"}}
	want := decl + root + `<TagSet>` +
		`<Tag><Key>env</Key><Value>it</Value></Tag>` +
		`<Tag><Key>a&amp;b</Key><Value>&lt;x&gt;</Value></Tag>` +
		`</TagSet></Tagging>`
	if got := string(AppendTagging(nil, tags)); got != want {
		t.Fatalf("AppendTagging =\n%s\nwant\n%s", got, want)
	}
}
