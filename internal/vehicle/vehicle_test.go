package vehicle

import "testing"

func TestCreateInputValidate(t *testing.T) {
	cases := []struct {
		name  string
		in    CreateInput
		field string
	}{
		{"missing code", CreateInput{Name: "Truk 1", Kind: KindTruck}, "code"},
		{"missing name", CreateInput{Code: "TRK-01", Kind: KindTruck}, "name"},
		{"unknown kind", CreateInput{Code: "TRK-01", Name: "Truk 1", Kind: "boat"}, "kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			in.Normalize()
			errs := in.Validate()
			if _, ok := errs[tc.field]; !ok {
				t.Fatalf("expected field %q to be rejected, got %v", tc.field, errs)
			}
		})
	}
}

func TestCreateInputNormalize(t *testing.T) {
	in := CreateInput{Code: "  TRK-01 ", Name: "  Truk 1 ", Kind: ""}
	in.Normalize()
	if in.Code != "TRK-01" || in.Name != "Truk 1" {
		t.Fatalf("text fields were not trimmed: %+v", in)
	}
	if in.Kind != KindTruck {
		t.Fatalf("expected default kind %q, got %q", KindTruck, in.Kind)
	}
	if errs := in.Validate(); len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestPositionInputValidate(t *testing.T) {
	lat, lng := -6.2, 106.8
	ok := PositionInput{Lat: &lat, Lng: &lng, Status: StatusMoving}
	ok.Normalize()
	if errs := ok.Validate(); len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}

	bad := 200.0
	badInput := PositionInput{Lat: &bad, Lng: &lng, Status: StatusMoving}
	badInput.Normalize()
	if _, ok := badInput.Validate()["lat"]; !ok {
		t.Fatal("expected lat out of range to be rejected")
	}

	missing := PositionInput{Lng: &lng, Status: StatusMoving}
	missing.Normalize()
	if _, ok := missing.Validate()["lat"]; !ok {
		t.Fatal("expected a missing lat to be rejected")
	}

	zero := 0.0
	zeroInput := PositionInput{Lat: &zero, Lng: &zero, Status: StatusMoving}
	zeroInput.Normalize()
	if errs := zeroInput.Validate(); len(errs) != 0 {
		t.Fatalf("zero is a valid coordinate, got %v", errs)
	}
}

func TestPositionInputNormalizeDefaultsStatus(t *testing.T) {
	lat, lng := 1.0, 2.0
	in := PositionInput{Lat: &lat, Lng: &lng}
	in.Normalize()
	if in.Status != StatusMoving {
		t.Fatalf("expected default status %q, got %q", StatusMoving, in.Status)
	}
}
