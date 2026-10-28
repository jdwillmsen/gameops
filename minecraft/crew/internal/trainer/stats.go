package trainer

import "math"

// pairedTest is a one-sided paired t-test of "the mean difference is above
// zero". The champion and a challenger fight the same scenarios, so pairing
// removes the scenario-to-scenario variance that would otherwise swamp the
// effect of a parameter change.
type pairedTest struct {
	N    int
	Mean float64
	SD   float64
	T    float64
	P    float64
}

func newPairedTest(diffs []float64) pairedTest {
	n := len(diffs)
	t := pairedTest{N: n, P: 1}
	if n == 0 {
		return t
	}
	for _, d := range diffs {
		t.Mean += d
	}
	t.Mean /= float64(n)
	if n < 2 {
		return t
	}
	var ss float64
	for _, d := range diffs {
		ss += (d - t.Mean) * (d - t.Mean)
	}
	t.SD = math.Sqrt(ss / float64(n-1))
	if t.SD == 0 {
		// The t statistic is undefined without variance, and zero variance is
		// no proof of certainty: n identical gains are what a deterministic
		// scorer shows. The sign test is what remains: n gains in a row
		// happen with probability 0.5^n if nothing changed.
		if t.Mean > 0 {
			t.T, t.P = math.Inf(1), math.Pow(0.5, float64(n))
		}
		return t
	}
	t.T = t.Mean / (t.SD / math.Sqrt(float64(n)))
	t.P = 1 - studentCDF(t.T, float64(n-1))
	return t
}

// studentCDF is the Student t cumulative distribution through the regularised
// incomplete beta function.
func studentCDF(t, df float64) float64 {
	if math.IsInf(t, 1) {
		return 1
	}
	if math.IsInf(t, -1) {
		return 0
	}
	x := df / (df + t*t)
	tail := 0.5 * regIncBeta(df/2, 0.5, x)
	if t > 0 {
		return 1 - tail
	}
	return tail
}

func regIncBeta(a, b, x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	front := math.Exp(lab - la - lb + a*math.Log(x) + b*math.Log(1-x))
	// The continued fraction converges fast on the side of the mean; swap
	// the arguments on the other side.
	if x < (a+1)/(a+b+2) {
		return front * betaCF(a, b, x) / a
	}
	return 1 - front*betaCF(b, a, 1-x)/b
}

// betaCF evaluates the incomplete beta continued fraction with Lentz's method.
func betaCF(a, b, x float64) float64 {
	const tiny = 1e-300
	qab, qap, qam := a+b, a+1, a-1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1 / d
	h := d
	for m := 1; m <= 300; m++ {
		fm := float64(m)
		m2 := 2 * fm
		aa := fm * (b - fm) * x / ((qam + m2) * (a + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		h *= d * c
		aa = -(a + fm) * (qab + fm) * x / ((a + m2) * (qap + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < 3e-12 {
			break
		}
	}
	return h
}
