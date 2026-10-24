// Package application owns runtime-independent values and, incrementally, application policy.
package application

import "time"

// ItemKind is the persisted kind of an ordered extraction item.
type ItemKind string

const (
	// Task is a deliverable task item.
	Task ItemKind = "task"
	// Note is a deliverable note item.
	Note ItemKind = "note"
)

// Item is the ordered application value consumed by extraction, storage, and delivery adapters.
// Field order and encoding remain compatible with the former root QueuedItem.
type Item struct {
	Kind         ItemKind
	Title        string
	Content      string
	Due          *time.Time
	AllDay       bool
	Priority     int
	Tags         []string
	ProjectAlias string
}

// Fingerprint identifies the stable recorded payload independently of provider routing.
type Fingerprint string

// ItemIdentity identifies one frozen ordered item independently of its remote encoding.
// Boundary validation remains required before an identity is used for delivery.
type ItemIdentity struct {
	Fingerprint Fingerprint
	Index       int
}
