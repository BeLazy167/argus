package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestParsePeriodInterval(t *testing.T) {
	tests := []struct {
		query string
		want  int32
	}{
		{"7d", 7},
		{"30d", 30},
		{"90d", 90},
		{"", 30},
		{"1d", 30},
		{"365d", 30},
		{"-7d", 30},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/stats/overview?period="+tt.query, nil)
			got := parsePeriodInterval(r)
			if got.Days != tt.want {
				t.Errorf("parsePeriodInterval(%q).Days = %d, want %d", tt.query, got.Days, tt.want)
			}
			if !got.Valid {
				t.Errorf("parsePeriodInterval(%q).Valid = false", tt.query)
			}
		})
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want float64
	}{
		{"float64", float64(3.14), 3.14},
		{"float32", float32(2.5), 2.5},
		{"int", int(42), 42},
		{"int32", int32(7), 7},
		{"int64", int64(99), 99},
		{"json.Number", json.Number("1.23"), 1.23},
		{"nil", nil, 0},
		{"string", "hello", 0},
		{"bool", true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toFloat64(tt.val)
			// float32→float64 has imprecision
			if tt.name == "float32" {
				if got < 2.49 || got > 2.51 {
					t.Errorf("toFloat64(%v) = %v, want ~%v", tt.val, got, tt.want)
				}
				return
			}
			if got != tt.want {
				t.Errorf("toFloat64(%v) = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}

func TestParsePeriodIntervalType(t *testing.T) {
	r, _ := http.NewRequest("GET", "/stats/overview?period=90d", nil)
	got := parsePeriodInterval(r)
	want := pgtype.Interval{Days: 90, Valid: true}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
