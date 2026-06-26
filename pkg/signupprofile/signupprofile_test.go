package signupprofile

import "testing"

func TestGrade(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Profile
		want string
	}{
		{"explicit grade", Profile{ID: "x", GradeID: "habitant"}, "habitant"},
		{"default grade", Profile{ID: "y"}, DefaultGrade},
	} {
		if got := tc.p.Grade(); got != tc.want {
			t.Errorf("%s: Grade() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestFind(t *testing.T) {
	catalog := []Profile{
		{ID: "adherent", Label: "Adhérent"},
		{ID: "don", Label: "Donateur", Extra: []ExtraField{{Name: "telephone", Type: "tel"}}},
	}
	for _, tc := range []struct {
		id      string
		wantOK  bool
		wantLbl string
	}{
		{"adherent", true, "Adhérent"},
		{"don", true, "Donateur"},
		{"inconnu", false, ""},
	} {
		got, ok := Find(catalog, tc.id)
		if ok != tc.wantOK {
			t.Errorf("Find(%q) ok = %v, want %v", tc.id, ok, tc.wantOK)
		}
		if ok && got.Label != tc.wantLbl {
			t.Errorf("Find(%q) label = %q, want %q", tc.id, got.Label, tc.wantLbl)
		}
	}
}
