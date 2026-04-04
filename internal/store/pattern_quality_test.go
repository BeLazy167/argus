package store

import (
	"math"
	"testing"
)

func TestRecalculateQuality(t *testing.T) {
	tests := []struct {
		name      string
		confirmed int
		dismissed int
		want      float64
	}{
		{"zero_zero_returns_prior", 0, 0, 0.5},
		{"10_confirmed_0_dismissed", 10, 0, 0.8333},
		{"0_confirmed_10_dismissed", 0, 10, 0.1667},
		{"5_confirmed_5_dismissed", 5, 5, 0.5},
		{"1_confirmed_0_dismissed", 1, 0, 0.5833},
		{"0_confirmed_1_dismissed", 0, 1, 0.4167},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RecalculateQuality(tt.confirmed, tt.dismissed)
			if math.Abs(got-tt.want) > 0.001 {
				t.Errorf("RecalculateQuality(%d, %d) = %f, want ~%f", tt.confirmed, tt.dismissed, got, tt.want)
			}
		})
	}
}
