package sample

import "math"

type Sample interface {
	Update(v float64)
	Max() float64
	Mean() float64
	Count() int
}

type FixLengthSample struct {
	maxLength int
	values    []float64
}

func NewFixLengthSample(maxLength int) Sample {
	return &FixLengthSample{
		maxLength: maxLength,
		values:    make([]float64, 0),
	}
}

func (s *FixLengthSample) Update(v float64) {
	s.values = append(s.values, v)

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
