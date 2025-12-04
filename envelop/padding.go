package envelop

// AddPadding is a placeholder for future implementations.
// Currently, padding is handled implicitly because the Frame payload
// is always pre-initialized with zeros.
//
// If you want random padding for traffic obfuscation, implement here.
func AddPadding(b []byte, used int) {
	// For future: fill b[used:] with random bytes if needed.
}
