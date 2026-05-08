package gitwork

// Compile-time interface compliance checks.

// Verify Client implements repoInitializer at compile time.
var _ repoInitializer = &Client{}
