package service_test

import (
	"testing"

	"recurringjob/internal/service"
)

func TestValidateID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		ok   bool
	}{
		{"lowercase", "4aa72fb5-dbfd-41f1-91d9-7b2e6fabf0c2", true},
		{"uppercase", "4AA72FB5-DBFD-41F1-91D9-7B2E6FABF0C2", true},
		{"nil uuid", "00000000-0000-0000-0000-000000000000", true},
		{"empty", "", false},
		{"word", "not-a-uuid", false},
		{"too short", "4aa72fb5-dbfd-41f1-91d9-7b2e6fabf0c", false},
		{"too long", "4aa72fb5-dbfd-41f1-91d9-7b2e6fabf0c22", false},
		{"no dashes", "4aa72fb5dbfd41f191d97b2e6fabf0c2", false},
		{"non-hex", "zaa72fb5-dbfd-41f1-91d9-7b2e6fabf0c2", false},
		{"trailing junk", "4aa72fb5-dbfd-41f1-91d9-7b2e6fabf0c2 ", false},
		{"leading junk", "x4aa72fb5-dbfd-41f1-91d9-7b2e6fabf0c2", false},
		{"braces", "{4aa72fb5-dbfd-41f1-91d9-7b2e6fabf0c2}", false},
		{"urn prefix", "urn:uuid:4aa72fb5-dbfd-41f1-91d9-7b2e6fabf0c2", false},
		{"newline", "4aa72fb5-dbfd-41f1-91d9-7b2e6fabf0c2\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := service.ValidateID("job id", tt.id)
			if tt.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.ok && statusOf(t, err) != 400 {
				t.Fatalf("want 400 for %q, got %v", tt.id, err)
			}
		})
	}
	if err := service.ValidateID("exceptJobId", "x"); err == nil || err.Error() != "exceptJobId must be a UUID" {
		t.Fatalf("message: %v", err)
	}
}
