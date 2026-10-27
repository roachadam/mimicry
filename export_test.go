package mimicry

// SetPins replaces t's pins between requests, so a test can check that a
// later (resumed) handshake is held to them.
func SetPins(t *Transport, pins []Pin) error {
	set, err := parsePins(pins)
	if err != nil {
		return err
	}

	t.pins = set

	return nil
}
