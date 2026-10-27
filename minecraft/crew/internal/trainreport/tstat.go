package trainreport

import "math"

// tTwoSidedP is the two-tailed p value of Student's t at t with df degrees
// of freedom, via the regularised incomplete beta function; negative t
// mirrors positive. The verdict only needs p to a few decimals.
func tTwoSidedP(t, df float64) float64 {
	if df <= 0 || math.IsNaN(t) {
		return 2
	}
	return betainc(df/(df+t*t), df/2, 0.5)
}

// betainc is the regularised incomplete beta function I_x(a,b), the classic
// split: a continued fraction on one side of x, the mirrored one past it.
func betainc(x, a, b float64) float64 {
	switch {
	case x <= 0:
		return 0
	case x >= 1:
		return 1
	}
	bt := math.Exp(lgamma(a+b) - lgamma(a) - lgamma(b) +
		a*math.Log(x) + b*math.Log(1-x))
	if x < (a+1)/(a+b+2) {
		return bt * betacf(a, b, x) / a
	}
	return 1 - bt*betacf(b, a, 1-x)/b
}

// betacf is the continued fraction of the incomplete beta function; the
// even/odd steps each append one fraction term with d as denominator chain
// and c as numerator chain, per the standard Lentz-style evaluation.
func betacf(a, b, x float64) float64 {
	const maxIter = 200
	const eps = 3e-12
	const fpmin = 1e-300
	qab, qap, qam := a+b, a+1, a-1
	c, d := 1.0, 1.0-qab*x/qap
	if math.Abs(d) < fpmin {
		d = fpmin
	}
	d = 1 / d
	h := d
	for m := 1.0; m <= maxIter; m++ {
		m2 := 2 * m
		aa := m * (b - m) * x / ((qam + m2) * (a + m2))
		d = 1 + aa*d
		if math.Abs(d) < fpmin {
			d = fpmin
		}
		c = 1 + aa/c
		if math.Abs(c) < fpmin {
			c = fpmin
		}
		d = 1 / d
		h *= d * c
		aa = -(a + m) * (qab + m) * x / ((a + m2) * (qap + m2))
		d = 1 + aa*d
		if math.Abs(d) < fpmin {
			d = fpmin
		}
		c = 1 + aa/c
		if math.Abs(c) < fpmin {
			c = fpmin
		}
		d = 1 / d
		delta := d * c
		h *= delta
		if math.Abs(delta-1) < eps {
			break
		}
	}
	return h
}

// lgamma keeps the Lgamma two-return form out of the expressions above.
func lgamma(v float64) float64 {
	g, _ := math.Lgamma(v)
	return g
}

// tcrit95 inverts the Student's t distribution at 97.5% by bisection; the
// CDF is strictly increasing in t so a wide bracket converges in steps.
func tcrit95(df float64) float64 {
	lo, hi := 0.0, 1000.0
	for i := 0; i < 100; i++ {
		mid := (lo + hi) / 2
		if 1-tTwoSidedP(mid, df)/2 < 0.975 {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}
