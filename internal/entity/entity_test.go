package entity

import (
	"strings"
	"testing"
)

func validInput() EntityInput {
	return EntityInput{
		Name:      "Monas",
		Type:      "monument",
		Status:    "active",
		Latitude:  -6.1754,
		Longitude: 106.8272,
	}
}

func TestValidateOK(t *testing.T) {
	in := validInput()
	if fields := in.Validate(); len(fields) != 0 {
		t.Fatalf("expected valid input, got %v", fields)
	}
}

func TestValidateBlankNameAndType(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*EntityInput)
		field  string
	}{
		{"empty name", func(in *EntityInput) { in.Name = "" }, "name"},
		{"spaces name", func(in *EntityInput) { in.Name = "   " }, "name"},
		{"empty type", func(in *EntityInput) { in.Type = "" }, "type"},
		{"spaces type", func(in *EntityInput) { in.Type = " \t " }, "type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			tc.mutate(&in)
			if fields := in.Validate(); fields[tc.field] == "" {
				t.Fatalf("expected %q error, got %v", tc.field, fields)
			}
		})
	}
}

func TestValidateStatus(t *testing.T) {
	for _, s := range []string{"active", "inactive"} {
		in := validInput()
		in.Status = s
		if fields := in.Validate(); len(fields) != 0 {
			t.Fatalf("status %q should be valid, got %v", s, fields)
		}
	}
	for _, s := range []string{"", "ACTIVE", "deleted", "unknown"} {
		in := validInput()
		in.Status = s
		if fields := in.Validate(); fields["status"] == "" {
			t.Fatalf("status %q should be invalid", s)
		}
	}
}

func TestValidateCoordinates(t *testing.T) {
	cases := []struct {
		name      string
		lat, lon  float64
		field     string
		wantValid bool
	}{
		{"origin", 0, 0, "", true},
		{"min bounds", -90, -180, "", true},
		{"max bounds", 90, 180, "", true},
		{"lat too low", -90.0001, 0, "latitude", false},
		{"lat too high", 90.0001, 0, "latitude", false},
		{"lon too low", 0, -180.0001, "longitude", false},
		{"lon too high", 0, 180.0001, "longitude", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			in.Latitude, in.Longitude = tc.lat, tc.lon
			fields := in.Validate()
			if tc.wantValid && len(fields) != 0 {
				t.Fatalf("expected valid, got %v", fields)
			}
			if !tc.wantValid && fields[tc.field] == "" {
				t.Fatalf("expected %q error, got %v", tc.field, fields)
			}
		})
	}
}

func TestValidateLengthLimits(t *testing.T) {
	in := validInput()
	in.Name = strings.Repeat("a", 256)
	if fields := in.Validate(); fields["name"] == "" {
		t.Fatalf("expected name length error, got %v", fields)
	}

	in = validInput()
	in.Type = strings.Repeat("b", 101)
	if fields := in.Validate(); fields["type"] == "" {
		t.Fatalf("expected type length error, got %v", fields)
	}
}

func TestNormalize(t *testing.T) {
	in := EntityInput{Name: "  Monas ", Type: " monument\t", Status: ""}
	in.Normalize()
	if in.Name != "Monas" || in.Type != "monument" {
		t.Fatalf("expected trimmed values, got %+v", in)
	}
	if in.Status != StatusActive {
		t.Fatalf("expected default status %q, got %q", StatusActive, in.Status)
	}
	if fields := in.Validate(); len(fields) != 0 {
		t.Fatalf("normalized input should validate, got %v", fields)
	}
}
