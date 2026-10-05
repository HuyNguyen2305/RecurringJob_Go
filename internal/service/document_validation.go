package service

import (
	"strings"
	"unicode/utf8"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
)

// Limits chosen so that quantity * price * items cannot overflow an int64.
const (
	MaxLineItems      = 100
	MaxQuantity       = 1_000_000
	MaxUnitPriceCents = 10_000_000_000 // 100 million units of currency
	MaxNotesLen       = 2000
	MaxDescriptionLen = 500
)

// LineItemInput is one line of a document as submitted.
type LineItemInput struct {
	Description    string
	Quantity       int
	UnitPriceCents int64
}

// DocumentInput is the content of an estimate or invoice: notes and lines.
// Who it is for comes from the estimate's references or from the job.
type DocumentInput struct {
	Notes     string
	LineItems []LineItemInput
}

// EstimateInput creates an estimate for a customer's location and a service type.
type EstimateInput struct {
	CustomerID    string
	LocationID    string
	ServiceTypeID string
	DocumentInput
}

// DocumentPatch is a partial edit; nil fields stay unchanged and a non-nil
// LineItems replaces every line item. The reference ids are only for
// estimates that have no job yet.
type DocumentPatch struct {
	Notes         *string
	LineItems     *[]LineItemInput
	CustomerID    *string
	LocationID    *string
	ServiceTypeID *string
}

// hasRefs reports whether the patch changes customer, location or service type.
func (p DocumentPatch) hasRefs() bool {
	return p.CustomerID != nil || p.LocationID != nil || p.ServiceTypeID != nil
}

func validateNotes(s string) (string, error) {
	if utf8.RuneCountInString(s) > MaxNotesLen {
		return "", bad("notes must be at most %d characters", MaxNotesLen)
	}
	return s, nil
}

// buildLineItems validates items and returns them with positions 0..n-1.
func buildLineItems(items []LineItemInput) ([]model.CustomerLineItem, error) {
	if len(items) > MaxLineItems {
		return nil, bad("a document can have at most %d line items", MaxLineItems)
	}
	out := make([]model.CustomerLineItem, 0, len(items))
	for i, it := range items {
		desc := strings.TrimSpace(it.Description)
		switch {
		case desc == "":
			return nil, bad("lineItems[%d].description is required", i)
		case utf8.RuneCountInString(desc) > MaxDescriptionLen:
			return nil, bad("lineItems[%d].description must be at most %d characters", i, MaxDescriptionLen)
		case it.Quantity < 1 || it.Quantity > MaxQuantity:
			return nil, bad("lineItems[%d].quantity must be between 1 and %d", i, MaxQuantity)
		case it.UnitPriceCents < 0 || it.UnitPriceCents > MaxUnitPriceCents:
			return nil, bad("lineItems[%d].unitPriceCents must be between 0 and %d", i, int64(MaxUnitPriceCents))
		}
		out = append(out, model.CustomerLineItem{
			Position: i, Description: desc, Quantity: it.Quantity, UnitPriceCents: it.UnitPriceCents,
		})
	}
	return out, nil
}

// buildDocument validates a creation request and returns a draft document
// (type, references and job fields are left to the caller).
func buildDocument(in DocumentInput) (*model.CustomerDocument, error) {
	notes, err := validateNotes(in.Notes)
	if err != nil {
		return nil, err
	}
	items, err := buildLineItems(in.LineItems)
	if err != nil {
		return nil, err
	}
	return &model.CustomerDocument{Status: DocStatusDraft, Notes: notes, LineItems: items}, nil
}

// buildPatch validates a partial edit and returns the column updates plus the
// replacement line items (nil when the items are unchanged). The reference
// ids are validated by the estimate service, not here.
func buildPatch(p DocumentPatch) (map[string]any, []model.CustomerLineItem, error) {
	fields := map[string]any{}
	if p.Notes != nil {
		v, err := validateNotes(*p.Notes)
		if err != nil {
			return nil, nil, err
		}
		fields["notes"] = v
	}
	var items []model.CustomerLineItem
	if p.LineItems != nil {
		var err error
		if items, err = buildLineItems(*p.LineItems); err != nil {
			return nil, nil, err
		}
	}
	if len(fields) == 0 && p.LineItems == nil && !p.hasRefs() {
		return nil, nil, apperror.Validation("nothing to update")
	}
	return fields, items, nil
}
