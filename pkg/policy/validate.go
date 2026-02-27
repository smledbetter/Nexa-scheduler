package policy

import "fmt"

// validPrivacyLevels lists the allowed values for DefaultPrivacy.
var validPrivacyLevels = map[string]bool{
	"":         true,
	"standard": true,
	"high":     true,
}

// Validate checks that a Policy has valid field values.
// Returns a descriptive error for the first invalid field found.
func Validate(pol *Policy) error {
	if !validPrivacyLevels[pol.Privacy.DefaultPrivacy] {
		return fmt.Errorf(
			"privacyPolicy.defaultPrivacy %q is invalid; must be one of: \"high\", \"standard\", or empty",
			pol.Privacy.DefaultPrivacy,
		)
	}
	return nil
}
