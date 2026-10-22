package xmlenc

import "testing"

func TestAppendError(t *testing.T) {
	tests := []struct {
		name string
		in   ErrorResponse
		want string
	}{
		{
			name: "all fields",
			in: ErrorResponse{
				Code:      "NoSuchKey",
				Message:   "The specified key does not exist.",
				Resource:  "/bucket/key",
				RequestID: "req1",
				HostID:    "host1",
			},
			want: wantDecl + `<Error>` +
				`<Code>NoSuchKey</Code><Message>The specified key does not exist.</Message>` +
				`<Resource>/bucket/key</Resource><RequestId>req1</RequestId><HostId>host1</HostId>` +
				`</Error>`,
		},
		{
			name: "empty fields are still emitted and root has no namespace",
			in:   ErrorResponse{},
			want: wantDecl + `<Error>` +
				`<Code></Code><Message></Message><Resource></Resource><RequestId></RequestId><HostId></HostId>` +
				`</Error>`,
		},
		{
			name: "values are escaped",
			in: ErrorResponse{
				Code:     "InvalidArgument",
				Message:  `a < b & "c"`,
				Resource: "/b/k&1",
			},
			want: wantDecl + `<Error>` +
				`<Code>InvalidArgument</Code><Message>a &lt; b &amp; &#34;c&#34;</Message>` +
				`<Resource>/b/k&amp;1</Resource><RequestId></RequestId><HostId></HostId>` +
				`</Error>`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkAppend(t, tc.want, func(dst []byte) []byte { return AppendError(dst, &tc.in) })
		})
	}
}
