package admin

import (
	"strings"
	"testing"
)

func TestProfileForm_Validate(t *testing.T) {
	tests := []struct {
		name string
		form profileForm
		want []string // field names expected to be invalid
	}{
		{"empty is fine", profileForm{}, nil},
		{"typical", profileForm{FirstName: "Amara", LastName: "Okafor", Username: "amara_o", Phone: "+2348025550142", Locale: "en", Timezone: "Africa/Lagos"}, nil},
		{"locale with region", profileForm{Locale: "fr-CA"}, nil},
		{"username too short", profileForm{Username: "ab"}, []string{"username"}},
		{"username too long", profileForm{Username: strings.Repeat("a", 31)}, []string{"username"}},
		{"username with space", profileForm{Username: "two words"}, []string{"username"}},
		{"username with markup", profileForm{Username: "<b>bold</b>"}, []string{"username"}},
		{"phone without digits", profileForm{Phone: "call me"}, []string{"phone"}},
		{"phone with newline", profileForm{Phone: "+33612345678\nBcc: x"}, []string{"phone"}},
		{"phone too short", profileForm{Phone: "+123"}, []string{"phone"}},
		{"locale garbage", profileForm{Locale: "english please"}, []string{"locale"}},
		{"timezone with spaces", profileForm{Timezone: "Europe/ Paris"}, []string{"timezone"}},
		{"timezone too long", profileForm{Timezone: strings.Repeat("a", 65)}, []string{"timezone"}},
		{"first name too long", profileForm{FirstName: strings.Repeat("é", 101)}, []string{"first_name"}},
		{"100 multibyte characters is fine", profileForm{FirstName: strings.Repeat("é", 100)}, nil},
		{"several at once", profileForm{Username: "x", Phone: "y"}, []string{"username", "phone"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.form.validate()
			if len(got) != len(tt.want) {
				t.Fatalf("invalid fields = %v, want %v", got, tt.want)
			}
			for _, field := range tt.want {
				if got[field] == "" {
					t.Errorf("expected an error for %q, got %v", field, got)
				}
			}
		})
	}
}
