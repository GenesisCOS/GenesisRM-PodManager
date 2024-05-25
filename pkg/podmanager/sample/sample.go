package sample

import (
	"math"

	stat "gonum.org/v1/gonum/stat"
)

type Sample interface {
	Update(v float64)
	Max() float64
	Stdev() float64
	Mean() float64
	Count() int
	Last() float64
}

type FixLengthSample struct {
	maxLength int
	values    []float64
	lastValue float64
}

func NewFixLengthSample(maxLength int) Sample {
	return &FixLengthSample{
		maxLength: maxLength,
		values:    make([]float64, 0),
	}
}

func (s *FixLengthSample) Stdev() float64 {
	return stat.StdDev(s.values, nil)
}

func (s *FixLengthSample) Last() float64 {
	return s.lastValue
}

func (s *FixLengthSample) Update(v float64) {
	s.values = append(s.values, v)
	s.lastValue = v
	if len(s.values) > s.maxLength {
		length := len(s.values)
		s.values = s.values[length-s.maxLength : length]
	}
}

func (s *FixLengthSample) Max() float64 {
	if len(s.values) == 0 {
		return 0
	}
	max := float64(math.MinInt64)
	for _, v := range s.values {
		if max < v {
			max = v
		}
	}
	return max
}

func (s *FixLengthSample) Sum() float64 {
	var sum float64 = 0
	for _, v := range s.values {
		sum += v
	}
	return sum
}

func (s *FixLengthSample) Mean() float64 {
	if len(s.values) == 0 {
		return 0.0
	}
	return s.Sum() / float64(s.Count())
}

func (s *FixLengthSample) Count() int {
	return len(s.values)
}
