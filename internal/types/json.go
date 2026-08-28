package types

import (
	"encoding/json"
	"errors"
)

// MarshalJSON encodes the Jones vector as a 4-element float array
// [Re(Ex), Im(Ex), Re(Ey), Im(Ey)], matching the YAML sequence form.
// complex128 has no standard JSON encoding, so this method is required
// wherever a Jones vector appears in JSON output (e.g. `list rays --json`).
func (j JonesVector) MarshalJSON() ([]byte, error) {
	return json.Marshal([]float64{real(j.Ex), imag(j.Ex), real(j.Ey), imag(j.Ey)})
}

// UnmarshalJSON decodes the 4-element float array produced by MarshalJSON.
func (j *JonesVector) UnmarshalJSON(data []byte) error {
	var parts []float64
	if err := json.Unmarshal(data, &parts); err != nil {
		return err
	}
	if len(parts) != 4 {
		return errors.New("JonesVector requires exactly 4 elements: [Re(Ex), Im(Ex), Re(Ey), Im(Ey)]")
	}
	j.Ex = complex(parts[0], parts[1])
	j.Ey = complex(parts[2], parts[3])
	return nil
}