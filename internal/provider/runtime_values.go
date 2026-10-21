package provider

import "math"

func runtimeNumber(value any) (float64, bool) {
	number, ok := value.(float64)
	if !ok || number < 0 {
		return 0, false
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	return number, true
}

func contextTokens(value any) (int64, bool) {
	number, ok := runtimeNumber(value)
	if !ok {
		return 0, false
	}
	if number >= float64(math.MaxInt64) || number != math.Trunc(number) {
		return 0, false
	}
	return int64(number), true
}
