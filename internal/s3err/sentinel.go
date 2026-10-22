package s3err

// Coder is implemented by errors that know their S3 code.
type Coder interface {
	error
	S3Code() Code
}

// Sentinel is an internal error value with a fixed S3 code. Compare with
// errors.Is; sentinels are matched by identity.
type Sentinel struct {
	msg  string
	code Code
}

// NewSentinel declares a sentinel error. Domain packages use it for errors
// such as "write quorum not reached" (ServiceUnavailable) so that CodeOf can
// classify them without importing the domain.
func NewSentinel(msg string, code Code) *Sentinel {
	return &Sentinel{msg: msg, code: code}
}

func (s *Sentinel) Error() string { return s.msg }

// S3Code implements Coder.
func (s *Sentinel) S3Code() Code { return s.code }
