package service_test

import (
	"reflect"
	"sort"
	"testing"

	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

func TestCanTransitionDocument(t *testing.T) {
	est, inv := model.DocTypeEstimate, model.DocTypeInvoice
	tests := []struct {
		typ, from, to string
		want          bool
	}{
		{est, "draft", "sent", true},
		{est, "draft", "declined", true},
		{est, "sent", "declined", true},
		{est, "sent", "approved", false}, // approval has its own endpoint
		{est, "draft", "approved", false},
		{est, "approved", "declined", false},
		{est, "declined", "sent", false},
		{est, "draft", "paid", false},
		{inv, "draft", "sent", true},
		{inv, "draft", "void", true},
		{inv, "sent", "paid", true},
		{inv, "sent", "void", true},
		{inv, "draft", "paid", false},
		{inv, "paid", "void", false},
		{inv, "paid", "refunded", true},
		{inv, "refunded", "paid", false},
		{inv, "refunded", "void", false},
		{inv, "sent", "refunded", false},
		{inv, "draft", "refunded", false},
		{est, "sent", "refunded", false},
		{inv, "void", "sent", false},
		{inv, "sent", "draft", false},
		{inv, "sent", "approved", false},
		{"other", "draft", "sent", false},
	}
	for _, tt := range tests {
		if got := service.CanTransitionDocument(tt.typ, tt.from, tt.to); got != tt.want {
			t.Errorf("%s %s->%s = %v, want %v", tt.typ, tt.from, tt.to, got, tt.want)
		}
	}
}

func TestDocumentAllowedFrom(t *testing.T) {
	sorted := func(s []string) []string { sort.Strings(s); return s }
	tests := []struct {
		typ, to string
		want    []string
	}{
		{model.DocTypeInvoice, "void", []string{"draft", "sent"}},
		{model.DocTypeInvoice, "paid", []string{"sent"}},
		{model.DocTypeInvoice, "refunded", []string{"paid"}},
		{model.DocTypeInvoice, "sent", []string{"draft"}},
		{model.DocTypeEstimate, "declined", []string{"draft", "sent"}},
		{model.DocTypeEstimate, "approved", nil},
		{model.DocTypeInvoice, "draft", nil},
	}
	for _, tt := range tests {
		if got := sorted(service.DocumentAllowedFrom(tt.typ, tt.to)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s -> %s: %v, want %v", tt.typ, tt.to, got, tt.want)
		}
	}
}

func TestIsKnownDocumentStatus(t *testing.T) {
	tests := []struct {
		typ, s string
		want   bool
	}{
		{model.DocTypeEstimate, "approved", true},
		{model.DocTypeEstimate, "declined", true},
		{model.DocTypeEstimate, "paid", false},
		{model.DocTypeInvoice, "paid", true},
		{model.DocTypeInvoice, "void", true},
		{model.DocTypeInvoice, "refunded", true},
		{model.DocTypeEstimate, "refunded", false},
		{model.DocTypeInvoice, "approved", false},
		{model.DocTypeInvoice, "", false},
		{"other", "draft", false},
	}
	for _, tt := range tests {
		if got := service.IsKnownDocumentStatus(tt.typ, tt.s); got != tt.want {
			t.Errorf("%s %q = %v, want %v", tt.typ, tt.s, got, tt.want)
		}
	}
}

func TestDocumentEditableFrom(t *testing.T) {
	tests := []struct {
		typ  string
		want []string
	}{
		{model.DocTypeEstimate, []string{"draft", "sent", "approved"}},
		{model.DocTypeInvoice, []string{"draft"}},
		{"other", nil},
	}
	for _, tt := range tests {
		if got := service.DocumentEditableFrom(tt.typ); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: %v, want %v", tt.typ, got, tt.want)
		}
	}

	// A caller changing its copy must not change the rules.
	got := service.DocumentEditableFrom(model.DocTypeInvoice)
	got[0] = "paid"
	if service.DocumentEditableFrom(model.DocTypeInvoice)[0] != "draft" {
		t.Fatal("DocumentEditableFrom returned the internal slice")
	}

	// Final statuses are never editable.
	for _, s := range service.DocumentEditableFrom(model.DocTypeEstimate) {
		if s == "declined" {
			t.Error("a declined estimate must be final")
		}
	}
	for _, s := range service.DocumentEditableFrom(model.DocTypeInvoice) {
		if s == "sent" || s == "paid" || s == "void" {
			t.Errorf("an invoice must not be editable once %s", s)
		}
	}
}
