package xmldec

import (
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestParseTagging(t *testing.T) {
	const ok = `<?xml version="1.0" encoding="UTF-8"?>
<Tagging xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <TagSet>
    <Tag><Key>env</Key><Value>it</Value></Tag>
    <Tag><Key> spaced </Key><Value></Value></Tag>
  </TagSet>
</Tagging>`
	tags, err := ParseTagging([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	want := object.TagSet{{Key: "env", Value: "it"}, {Key: " spaced ", Value: ""}}
	if len(tags) != len(want) || tags[0] != want[0] || tags[1] != want[1] {
		t.Fatalf("tags = %+v, want %+v", tags, want)
	}

	empty, err := ParseTagging([]byte(`<Tagging><TagSet/></Tagging>`))
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty TagSet = %v, %v; want no tags and no error", empty, err)
	}

	cases := []struct {
		name string
		body string
		code s3err.Code
	}{
		{"empty", "", s3err.MalformedXML},
		{"wrong root", `<Delete><TagSet/></Delete>`, s3err.MalformedXML},
		{"no TagSet", `<Tagging/>`, s3err.MalformedXML},
		{"two TagSet", `<Tagging><TagSet/><TagSet/></Tagging>`, s3err.MalformedXML},
		{"unknown element in TagSet", `<Tagging><TagSet><Bogus/></TagSet></Tagging>`, s3err.MalformedXML},
		{"unknown element in Tag", `<Tagging><TagSet><Tag><Key>a</Key><Value>b</Value><Bogus/></Tag></TagSet></Tagging>`, s3err.MalformedXML},
		{"missing Key", `<Tagging><TagSet><Tag><Value>b</Value></Tag></TagSet></Tagging>`, s3err.MalformedXML},
		{"missing Value", `<Tagging><TagSet><Tag><Key>a</Key></Tag></TagSet></Tagging>`, s3err.MalformedXML},
		{"repeated Value", `<Tagging><TagSet><Tag><Key>a</Key><Value>b</Value><Value>c</Value></Tag></TagSet></Tagging>`, s3err.MalformedXML},
		{"text in TagSet", `<Tagging><TagSet>junk<Tag><Key>a</Key><Value>b</Value></Tag></TagSet></Tagging>`, s3err.MalformedXML},
		{"nested element in Key", `<Tagging><TagSet><Tag><Key><b>a</b></Key><Value>b</Value></Tag></TagSet></Tagging>`, s3err.MalformedXML},
		{"doctype rejected", `<!DOCTYPE x [<!ENTITY e "y">]><Tagging><TagSet><Tag><Key>a</Key><Value>&e;</Value></Tag></TagSet></Tagging>`, s3err.MalformedXML},
		{"entity expansion", `<!DOCTYPE d [<!ENTITY a "aaaa">]><Tagging><TagSet><Tag><Key>a</Key><Value>&a;</Value></Tag></TagSet></Tagging>`, s3err.MalformedXML},
		{"trailing garbage", `<Tagging><TagSet/></Tagging><x/>`, s3err.MalformedXML},
		{"truncated", `<Tagging><TagSet><Tag><Key>a`, s3err.MalformedXML},
		{"deep nesting", `<Tagging><TagSet>` + strings.Repeat("<Tag>", 1000), s3err.MalformedXML},
		{"eleven tags", tagElements(object.MaxTags + 1), s3err.InvalidTag},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseTagging([]byte(tc.body)); !s3err.Is(err, tc.code) {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
}

// tagElements returns a Tagging document of n Tag elements with distinct keys.
func tagElements(n int) string {
	var b strings.Builder
	b.WriteString(`<Tagging><TagSet>`)
	for i := 0; i < n; i++ {
		b.WriteString(`<Tag><Key>k`)
		b.WriteString(itoa(i))
		b.WriteString(`</Key><Value>v</Value></Tag>`)
	}
	b.WriteString(`</TagSet></Tagging>`)
	return b.String()
}
