package notification

import "net/http"

// SourceObject is the page source type of a cluster object or its projection
// (KOSD-C-53).
const SourceObject = "object"

// Page is a page the product declares (KOSD-C-22, KOSD-C-53). The shape is the
// framework's declaration shape: the product adds no field.
type Page struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	Location       string     `json:"location"`
	ReadOnly       bool       `json:"readOnly,omitempty"`
	ReadOnlyReason string     `json:"readOnlyReason,omitempty"`
	Items          []PageItem `json:"items"`
	Actions        []Action   `json:"actions,omitempty"`
}

// PageItem is one display item (KOSD-C-53): its source type, the projection it
// reads, and either the modification entry or the reason no entry exists.
type PageItem struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Source    string `json:"source"`
	Reference string `json:"reference,omitempty"`
	Action    string `json:"action,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Action is one executable write the page offers (KOSD-C-22). The browser
// submits it through the control-plane API with its own session.
type Action struct {
	ID           string `json:"id"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	RequiredRole string `json:"requiredRole,omitempty"`
}

// Pages returns the notification page (NOTIFY-C-7): the channel and
// subscription items carry their modification entries, and the record item
// states why it has none.
func Pages() []Page {
	return []Page{notificationPage()}
}

func notificationPage() Page {
	return Page{
		ID:       PageNotification,
		Title:    "通知",
		Location: "panel",
		Items: []PageItem{
			{
				ID: ItemChannels, Label: "渠道",
				Source: SourceObject, Reference: reference(ItemChannels),
				Action: ActionEditChannel,
			},
			{
				ID: ItemSubscriptions, Label: "订阅",
				Source: SourceObject, Reference: reference(ItemSubscriptions),
				Action: ActionEditSubscription,
			},
			{
				ID: ItemDeliveryRecords, Label: "投递记录",
				Source: SourceObject, Reference: reference(ItemDeliveryRecords),
				Reason: ReasonRecordsReadOnly,
			},
		},
		Actions: []Action{
			{
				ID: ActionCreateChannel, Method: http.MethodPost, Path: classPath(KindChannel),
				RequiredRole: string(RoleOwner),
			},
			{
				ID: ActionEditChannel, Method: http.MethodPut, Path: classPath(KindChannel) + "/{name}",
				RequiredRole: string(RoleOwner),
			},
			{
				ID: ActionDeleteChannel, Method: http.MethodDelete, Path: classPath(KindChannel) + "/{name}",
				RequiredRole: string(RoleOwner),
			},
			{
				ID: ActionCreateSubscription, Method: http.MethodPost, Path: classPath(KindSubscription),
				RequiredRole: string(RoleOwner),
			},
			{
				ID: ActionEditSubscription, Method: http.MethodPut, Path: classPath(KindSubscription) + "/{name}",
				RequiredRole: string(RoleOwner),
			},
			{
				ID: ActionDeleteSubscription, Method: http.MethodDelete, Path: classPath(KindSubscription) + "/{name}",
				RequiredRole: string(RoleOwner),
			},
		},
	}
}

// reference is the item reference the shell dispatches to this product's reader
// (KOSD-C-22).
func reference(item string) string { return Identifier + "/" + item }

// classPath is the control-plane route of one declared class (KOSD-C-12).
func classPath(kind string) string {
	return "/api/v1/resources/" + kind + "?apiVersion=" + APIGroup + "/" + ObjectVersion
}

// Reporting is the product's server-side projection of the page items
// (NOTIFY-C-7): every value comes from an object projection.
type Reporting struct {
	// Channels are the channel projections.
	Channels []Channel
	// Subscriptions are the subscription projections.
	Subscriptions []Subscription
	// Records are the delivery record projections.
	Records []Record
}

// Read returns the current value of one declared item, and false for an item
// this product does not declare. An empty projection is an empty current value,
// not an undeclared item (NOTIFY-C-7).
func (r Reporting) Read(item string) (any, bool) {
	switch item {
	case ItemChannels:
		return nonNil(r.Channels), true
	case ItemSubscriptions:
		return nonNil(r.Subscriptions), true
	case ItemDeliveryRecords:
		return nonNil(r.Records), true
	default:
		return nil, false
	}
}

// nonNil keeps an empty projection readable as an empty list (KOSD-C-53).
func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}
