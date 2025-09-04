package main

import (
	"github.com/bits-and-blooms/bloom/v3"
)

var (
	n  uint = 10000000
	fp      = 0.01
)

func main() {
	//filter := bloom.NewWithEstimates(n, fp)
	//filter := bloom.New(n, k) // load of 20, 5 keys

	m, k := bloom.EstimateParameters(n, fp)
	ActualfpRate := bloom.EstimateFalsePositiveRate(m, k, n)
	println(ActualfpRate)

	a := bloom.EstimateFalsePositiveRate(20*n, 5, n)
	if a > 0.001 {
		println("error")
	}
}
