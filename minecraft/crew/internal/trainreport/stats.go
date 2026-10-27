package trainreport

import "math"

// ci95 is a 95% interval on the mean from sum and sumsq, using the t
// distribution so small samples get honest intervals; n below 2 carries no
// width, so the point estimate is returned twice.
func ci95(mean, sumsq float64, n int) (lo, hi float64) {
	if n < 2 {
		return mean, mean
	}
	s2 := (sumsq - mean*mean*float64(n)) / float64(n-1)
	if !(s2 >= 0) {
		if s2 > -1e-9 {
			s2 = 0
		} else {
			return mean, mean
		}
	}
	hw := tcrit95(float64(n-1)) * math.Sqrt(s2/float64(n))
	return mean - hw, mean + hw
}

// ci95Values is the same interval over a slice of paired or bucketed values.
func ci95Values(vs []float64) (mean, lo, hi float64) {
	n := len(vs)
	if n == 0 {
		return 0, 0, 0
	}
	sum, sumsq := 0.0, 0.0
	for _, v := range vs {
		sum += v
		sumsq += v * v
	}
	mean = sum / float64(n)
	lo, hi = ci95(mean, sumsq, n)
	return mean, lo, hi
}
