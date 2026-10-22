package task

import (
	"strings"
	"testing"
)

func validInput() Input {
	userID := int64(7)
	placeID := int64(3)
	return Input{
		Title:          "Antar barang ke Gudang Utama",
		Description:    "Bawa 3 paletoli.",
		Status:         StatusPending,
		AssignedUserID: &userID,
		PlaceID:        &placeID,
		DueDate:        "2026-10-25",
	}
}

func TestValidateOK(t *testing.T) {
	in := validInput()
	if fields := in.Validate(); len(fields) != 0 {
		t.Fatalf("expected valid input, got %v", fields)
	}
}

func TestValidateBlankTitle(t *testing.T) {
	cases := []struct {
		name  string
		title string
	}{
		{"empty title", ""},
		{"spaces title", "   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			in.Title = tc.title
			if fields := in.Validate(); fields["title"] == "" {
				t.Fatalf("expected title error, got %v", fields)
			}
		})
	}
}

func TestValidateStatus(t *testing.T) {
	for _, s := range Statuses {
		in := validInput()
		in.Status = s
		if fields := in.Validate(); len(fields) != 0 {
			t.Fatalf("status %q should be valid, got %v", s, fields)
		}
	}
	for _, s := range []string{"", "ACTIVE", "done", "unknown"} {
		in := validInput()
		in.Status = s
		if fields := in.Validate(); fields["status"] == "" {
			t.Fatalf("status %q should be invalid", s)
		}
	}
}

func TestValidateLengthLimits(t *testing.T) {
	in := validInput()
	in.Title = strings.Repeat("a", MaxTitleLen+1)
	if fields := in.Validate(); fields["title"] == "" {
		t.Fatalf("expected title length error, got %v", fields)
	}

	in = validInput()
	in.Description = strings.Repeat("b", MaxDescriptionLen+1)
	if fields := in.Validate(); fields["description"] == "" {
		t.Fatalf("expected description length error, got %v", fields)
	}
}

func TestValidateDueDate(t *testing.T) {
	in := validInput()
	in.DueDate = "25-10-2026"
	if fields := in.Validate(); fields["due_date"] == "" {
		t.Fatalf("expected due_date error, got %v", fields)
	}

	// No deadline at all is fine: a work order without one is allowed.
	in = validInput()
	in.DueDate = ""
	if fields := in.Validate(); len(fields) != 0 {
		t.Fatalf("expected valid input without a deadline, got %v", fields)
	}
}

func TestValidateReferences(t *testing.T) {
	zero := int64(0)
	in := validInput()
	in.AssignedUserID = &zero
	if fields := in.Validate(); fields["assigned_user_id"] == "" {
		t.Fatalf("expected assigned_user_id error, got %v", fields)
	}

	in = validInput()
	in.PlaceID = &zero
	if fields := in.Validate(); fields["place_id"] == "" {
		t.Fatalf("expected place_id error, got %v", fields)
	}
}

func TestNormalize(t *testing.T) {
	in := Input{Title: "  Antar barang  ", Description: "  Bawa palet.  ", Status: "", DueDate: " 2026-10-25 "}
	in.Normalize()
	if in.Title != "Antar barang" {
		t.Fatalf("expected trimmed title, got %q", in.Title)
	}
	if in.Description != "Bawa palet." {
		t.Fatalf("expected trimmed description, got %q", in.Description)
	}
	if in.Status != StatusPending {
		t.Fatalf("expected default status %q, got %q", StatusPending, in.Status)
	}
	if fields := in.Validate(); len(fields) != 0 {
		t.Fatalf("normalized input should validate, got %v", fields)
	}
}

func TestStatusInput(t *testing.T) {
	in := StatusInput{Status: " IN_PROGRESS "}
	in.Normalize()
	if in.Status != StatusInProgress {
		t.Fatalf("expected %q, got %q", StatusInProgress, in.Status)
	}
	if fields := in.Validate(); len(fields) != 0 {
		t.Fatalf("expected valid status, got %v", fields)
	}

	in = StatusInput{Status: "done"}
	if fields := in.Validate(); fields["status"] == "" {
		t.Fatalf("expected status error, got %v", fields)
	}
}

func TestParseDate(t *testing.T) {
	d, err := ParseDate("2026-10-25")
	if err != nil {
		t.Fatalf("expected a date, got %v", err)
	}
	if d.Time.Year() != 2026 || d.Time.Month() != 10 || d.Time.Day() != 25 {
		t.Fatalf("expected 2026-10-25, got %v", d.Time)
	}

	if _, err := ParseDate("25/10/2026"); err == nil {
		t.Fatalf("expected an error for a date that is not ISO")
	}
}
