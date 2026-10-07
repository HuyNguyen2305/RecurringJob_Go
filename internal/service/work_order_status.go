package service

const (
	WOStatusDraft      = "draft"
	WOStatusScheduled  = "scheduled"
	WOStatusInProgress = "in_progress"
	WOStatusCompleted  = "completed"
	WOStatusCanceled   = "canceled"
)

// workOrderStatuses are all work order statuses.
var workOrderStatuses = []string{WOStatusDraft, WOStatusScheduled, WOStatusInProgress, WOStatusCompleted, WOStatusCanceled}

// workOrderTransitions lists the statuses each status may move to. Completed
// and canceled are final.
var workOrderTransitions = map[string][]string{
	WOStatusDraft:      {WOStatusScheduled, WOStatusCanceled},
	WOStatusScheduled:  {WOStatusInProgress, WOStatusCanceled},
	WOStatusInProgress: {WOStatusCompleted, WOStatusCanceled},
}

// workOrderEditableFrom are the statuses in which notes and tasks can change
// (tasks get ticked off while the work is in progress).
var workOrderEditableFrom = []string{WOStatusDraft, WOStatusScheduled, WOStatusInProgress}

// IsKnownWorkOrderStatus reports whether s is a work order status.
func IsKnownWorkOrderStatus(s string) bool { return contains(workOrderStatuses, s) }

// CanTransitionWorkOrder reports whether from -> to is allowed.
func CanTransitionWorkOrder(from, to string) bool {
	return contains(workOrderTransitions[from], to)
}

// WorkOrderAllowedFrom lists the statuses from which a change to `to` is
// allowed; it feeds the optimistic UPDATE guard.
func WorkOrderAllowedFrom(to string) []string {
	var out []string
	for _, from := range workOrderStatuses {
		if contains(workOrderTransitions[from], to) {
			out = append(out, from)
		}
	}
	return out
}
