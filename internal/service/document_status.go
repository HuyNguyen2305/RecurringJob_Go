package service

import "recurringjob/internal/model"

const (
	DocStatusDraft    = "draft"
	DocStatusSent     = "sent"
	DocStatusApproved = "approved" // estimate only
	DocStatusDeclined = "declined" // estimate only
	DocStatusPaid     = "paid"     // invoice only
	DocStatusRefunded = "refunded" // invoice only: a paid invoice whose money went back
	DocStatusVoid     = "void"     // invoice only
)

// documentTransitions lists, per document type, the statuses each status may
// move to through the status endpoint. An estimate reaches approved only
// through Approve, which also creates its job.
var documentTransitions = map[string]map[string][]string{
	model.DocTypeEstimate: {
		DocStatusDraft: {DocStatusSent, DocStatusDeclined},
		DocStatusSent:  {DocStatusDeclined},
	},
	model.DocTypeInvoice: {
		DocStatusDraft: {DocStatusSent, DocStatusVoid},
		DocStatusSent:  {DocStatusPaid, DocStatusVoid},
		DocStatusPaid:  {DocStatusRefunded},
	},
}

// documentEditableFrom lists the statuses in which a document's content can
// still be edited. An estimate stays editable after it is sent and approved
// (until an invoice for its job is paid); an invoice only while it is a draft.
// Declined and void documents are final.
var documentEditableFrom = map[string][]string{
	model.DocTypeEstimate: {DocStatusDraft, DocStatusSent, DocStatusApproved},
	model.DocTypeInvoice:  {DocStatusDraft},
}

// DocumentEditableFrom lists the statuses in which the document type can be edited.
func DocumentEditableFrom(docType string) []string {
	return append([]string(nil), documentEditableFrom[docType]...)
}

// estimateApprovableFrom are the estimate statuses Approve accepts.
var estimateApprovableFrom = []string{DocStatusDraft, DocStatusSent}

// documentStatuses are all statuses of a document type.
var documentStatuses = map[string][]string{
	model.DocTypeEstimate: {DocStatusDraft, DocStatusSent, DocStatusApproved, DocStatusDeclined},
	model.DocTypeInvoice:  {DocStatusDraft, DocStatusSent, DocStatusPaid, DocStatusRefunded, DocStatusVoid},
}

// IsKnownDocumentStatus reports whether s is a status of the document type.
func IsKnownDocumentStatus(docType, s string) bool {
	return contains(documentStatuses[docType], s)
}

// CanTransitionDocument reports whether from -> to is allowed through the
// status endpoint.
func CanTransitionDocument(docType, from, to string) bool {
	return contains(documentTransitions[docType][from], to)
}

// DocumentAllowedFrom lists the statuses from which a change to `to` is
// allowed; it feeds the optimistic UPDATE guard.
func DocumentAllowedFrom(docType, to string) []string {
	var out []string
	for from, tos := range documentTransitions[docType] {
		if contains(tos, to) {
			out = append(out, from)
		}
	}
	return out
}
