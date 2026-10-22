package auth

// Identity is an authenticated principal.
type Identity struct {
	AccessKey string
	User      string
	// Root is true for the root credentials, which bypass policies.
	Root bool
	// Anonymous is true for unsigned requests.
	Anonymous bool
}
