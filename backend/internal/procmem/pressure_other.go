//go:build !darwin

package procmem

func ReadPressure() (Pressure, error) {
	sys, err := ReadSystem()
	if err != nil {
		return Pressure{}, err
	}
	return Pressure{Raw: sys.PressureRaw, Source: sys.PressureSource}, nil
}
