package core

// StringPtr returns a pointer to the given string value.
// This is useful for distinguishing between "not provided" (nil) and "provided as empty string" (ptr("")).
//
//go:fix inline
func StringPtr(s string) *string { return new(s) }
