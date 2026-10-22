package sigv4

import (
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/s3err"
)

const goodSig = "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"

func goodAuth(cred, signed, sig string) string {
	return "AWS4-HMAC-SHA256 Credential=" + cred + ", SignedHeaders=" + signed + ", Signature=" + sig
}

func TestParseAuthorizationAccepts(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"space after comma", goodAuth("AKIAEXAMPLE/20130524/us-east-1/s3/aws4_request", "host;x-amz-date", goodSig)},
		{"no space after comma", "AWS4-HMAC-SHA256 Credential=AKIAEXAMPLE/20130524/us-east-1/s3/aws4_request,SignedHeaders=host,Signature=" + goodSig},
		{"parameters in any order", "AWS4-HMAC-SHA256 Signature=" + goodSig + ", Credential=AKIAEXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := parseAuthorization(c.in); err != nil {
				t.Fatalf("rejected valid header: %v", err)
			}
		})
	}
}

func TestParseAuthorizationRejectsMalformed(t *testing.T) {
	const cred = "AKIAEXAMPLE/20130524/us-east-1/s3/aws4_request"
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"wrong scheme", "AWS4-HMAC-SHA512 Credential=" + cred + ", SignedHeaders=host, Signature=" + goodSig},
		{"v2 scheme", "AWS AKIAEXAMPLE:c2lnbmF0dXJl"},
		{"lower-case scheme", "aws4-hmac-sha256 Credential=" + cred + ", SignedHeaders=host, Signature=" + goodSig},
		{"missing signature", "AWS4-HMAC-SHA256 Credential=" + cred + ", SignedHeaders=host"},
		{"missing credential", "AWS4-HMAC-SHA256 SignedHeaders=host, Signature=" + goodSig},
		{"missing signed headers", "AWS4-HMAC-SHA256 Credential=" + cred + ", Signature=" + goodSig},
		{"duplicate signature", "AWS4-HMAC-SHA256 Credential=" + cred + ", SignedHeaders=host, Signature=" + goodSig + ", Signature=" + goodSig},
		{"unknown parameter", "AWS4-HMAC-SHA256 Credential=" + cred + ", SignedHeaders=host, Signature=" + goodSig + ", Extra=1"},
		{"lower-case parameter name", "AWS4-HMAC-SHA256 credential=" + cred + ", SignedHeaders=host, Signature=" + goodSig},
		{"parameter without value separator", "AWS4-HMAC-SHA256 Credential " + cred + ", SignedHeaders=host, Signature=" + goodSig},
		{"trailing comma", "AWS4-HMAC-SHA256 Credential=" + cred + ", SignedHeaders=host, Signature=" + goodSig + ","},
		{"upper-case signature", "AWS4-HMAC-SHA256 Credential=" + cred + ", SignedHeaders=host, Signature=" + strings.ToUpper(goodSig)},
		{"short signature", "AWS4-HMAC-SHA256 Credential=" + cred + ", SignedHeaders=host, Signature=abcd"},
		{"non-hex signature", "AWS4-HMAC-SHA256 Credential=" + cred + ", SignedHeaders=host, Signature=" + strings.Repeat("z", 64)},
		{"credential too few parts", "AWS4-HMAC-SHA256 Credential=AKIAEXAMPLE/20130524/us-east-1/s3, SignedHeaders=host, Signature=" + goodSig},
		{"credential extra part", "AWS4-HMAC-SHA256 Credential=AKIAEXAMPLE/20130524/us-east-1/s3/aws4_request/x, SignedHeaders=host, Signature=" + goodSig},
		{"credential wrong service", "AWS4-HMAC-SHA256 Credential=AKIAEXAMPLE/20130524/us-east-1/iam/aws4_request, SignedHeaders=host, Signature=" + goodSig},
		{"credential wrong terminator", "AWS4-HMAC-SHA256 Credential=AKIAEXAMPLE/20130524/us-east-1/s3/aws5_request, SignedHeaders=host, Signature=" + goodSig},
		{"credential bad date", "AWS4-HMAC-SHA256 Credential=AKIAEXAMPLE/2013052/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=" + goodSig},
		{"credential impossible date", "AWS4-HMAC-SHA256 Credential=AKIAEXAMPLE/20131324/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=" + goodSig},
		{"access key with slash", "AWS4-HMAC-SHA256 Credential=AKIA/EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=" + goodSig},
		{"empty access key", "AWS4-HMAC-SHA256 Credential=/20130524/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=" + goodSig},
		{"region with upper case", "AWS4-HMAC-SHA256 Credential=AKIAEXAMPLE/20130524/US-EAST-1/s3/aws4_request, SignedHeaders=host, Signature=" + goodSig},
		{"signed headers unsorted", goodAuth(cred, "x-amz-date;host", goodSig)},
		{"signed headers duplicate", goodAuth(cred, "host;host", goodSig)},
		{"signed headers upper case", goodAuth(cred, "Host", goodSig)},
		{"signed headers empty entry", goodAuth(cred, "host;;x-amz-date", goodSig)},
		{"signed headers without host", goodAuth(cred, "x-amz-date", goodSig)},
		{"signed headers empty", goodAuth(cred, "", goodSig)},
		{"oversized header", "AWS4-HMAC-SHA256 " + strings.Repeat("A", maxAuthorizationLen)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseAuthorization(c.in)
			if !s3err.Is(err, s3err.AuthorizationHeaderMalformed) {
				t.Fatalf("err = %v, want AuthorizationHeaderMalformed", err)
			}
		})
	}
}

func TestParseAuthorizationFieldValues(t *testing.T) {
	got, err := parseAuthorization(goodAuth("AKIAEXAMPLE/20130524/us-east-1/s3/aws4_request", "host;range;x-amz-date", goodSig))
	if err != nil {
		t.Fatal(err)
	}
	if got.scope.accessKey != "AKIAEXAMPLE" || got.scope.date != "20130524" || got.scope.region != "us-east-1" {
		t.Fatalf("scope = %+v", got.scope)
	}
	if strings.Join(got.signedHeaders, ",") != "host,range,x-amz-date" {
		t.Fatalf("signed headers = %v", got.signedHeaders)
	}
	if s := got.scope.String(); s != "20130524/us-east-1/s3/aws4_request" {
		t.Fatalf("scope string = %q", s)
	}
}

func TestParseAuthorizationNeverPanics(t *testing.T) {
	inputs := []string{
		"AWS4-HMAC-SHA256 ", "AWS4-HMAC-SHA256 =", "AWS4-HMAC-SHA256 ,,,", "AWS4-HMAC-SHA256 Credential=",
		"AWS4-HMAC-SHA256 Credential=/////, SignedHeaders=;;, Signature=",
	}
	for _, in := range inputs {
		if _, err := parseAuthorization(in); err == nil {
			t.Fatalf("accepted %q", in)
		}
	}
}
