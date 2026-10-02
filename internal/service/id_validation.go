package service

import (
	"regexp"

	"recurringjob/internal/common/apperror"
)

var uuidFormat = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidateID rejects an id that is not a canonical UUID with a 400, so a
// malformed id never reaches Postgres (where it would be a 500).
func ValidateID(name, id string) error {
	if !uuidFormat.MatchString(id) {
		return apperror.Validation(name + " must be a UUID")
	}
	return nil
}
