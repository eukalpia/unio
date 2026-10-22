package sigv4

import (
	"net/http"
	"time"
)

// signingTime is the request time used by a signature.
type signingTime struct {
	// t is the request time in UTC.
	t time.Time
	// amz is the time in ISO 8601 basic format, as it appears in the string
	// to sign.
	amz string
	// header names the header that carried the time, which must be signed:
	// "x-amz-date", or "date" when only a Date header was sent.
	header string
}

// parseAmzDate parses an ISO 8601 basic timestamp such as 20130524T000000Z.
func parseAmzDate(v string) (time.Time, error) {
	if len(v) != len(amzLayout) {
		return time.Time{}, malformed("malformed X-Amz-Date")
	}
	t, err := time.Parse(amzLayout, v)
	if err != nil {
		return time.Time{}, malformed("malformed X-Amz-Date")
	}
	return t.UTC(), nil
}

// requestTime reads the signing time from X-Amz-Date, or from Date when
// X-Amz-Date is absent. More than one value is malformed.
func requestTime(h http.Header) (signingTime, error) {
	if vals := h.Values("X-Amz-Date"); len(vals) > 0 {
		if len(vals) > 1 {
			return signingTime{}, malformed("duplicate X-Amz-Date")
		}
		t, err := parseAmzDate(vals[0])
		if err != nil {
			return signingTime{}, err
		}
		return signingTime{t: t, amz: vals[0], header: "x-amz-date"}, nil
	}
	vals := h.Values("Date")
	if len(vals) != 1 {
		return signingTime{}, malformed("request has no signing time")
	}
	t, err := http.ParseTime(vals[0])
	if err != nil {
		return signingTime{}, malformed("malformed Date header")
	}
	t = t.UTC()
	return signingTime{t: t, amz: t.Format(amzLayout), header: "date"}, nil
}
