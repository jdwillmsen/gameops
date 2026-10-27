package trainreport

import "math"

// ci95 is the half-width build of a 95% confidence interval on the mean from
// sum and sumsq; n below 2 has no width to speak of, so the point estimate
// is reported twice.
func ci95(mean, sumsq float64, n int) (lo, hi float64) {
	if n < 2 {
		return mean, mean
	}
	s2 := (sumsq - mean*mean*float64(n)) / float64(n-1)
	if s2 < 0 {
		if s2 > -1e-9 {
			s2 = 0
		} else {
			return mean, mean
		}
	}
	hw := 1.96 * math.Sqrt(s2/float64(n))
	return mean - hw, mean + hw
}
