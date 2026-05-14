package core

// StringPtr returns a pointer to the given string value.
// This is useful for distinguishing between "not provided" (nil) and "provided as empty string" (ptr("")).
func StringPtr(s string) *string { return &s }
