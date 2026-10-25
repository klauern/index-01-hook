package application

import (
	"fmt"
	"sort"
	"strings"
)

const (
	// MaxItems limits one ordered extraction. Provider response sizes remain adapter policy.
	MaxItems = 10
	// MaxTitleBytes limits a trimmed application item title in UTF-8 bytes.
	MaxTitleBytes = 500
	// MaxContentBytes limits application content before provider marker encoding.
	MaxContentBytes = 32 << 10
	// MaxAliasBytes limits a trimmed project alias in UTF-8 bytes.
	MaxAliasBytes = 100
	// MaxTags limits input tags before duplicate removal.
	MaxTags = 10
	// MaxTagBytes limits each trimmed tag in UTF-8 bytes.
	MaxTagBytes = 100
)

// ValidPriority accepts the persisted application priority values.
func ValidPriority(priority int) bool {
	switch priority {
	case 0, 1, 3, 5:
		return true
	default:
		return false
	}
}

// NormalizeTags trims tags and removes exact duplicates in input order.
// Validation occurs before duplicate removal. Tag case is preserved.
func NormalizeTags(tags []string) ([]string, error) {
	if len(tags) > MaxTags {
		return nil, fmt.Errorf("too many tags")
	}
	result := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, raw := range tags {
		tag := strings.TrimSpace(raw)
		if tag == "" || len(tag) > MaxTagBytes {
			return nil, fmt.Errorf("tag is invalid")
		}
		if _, duplicate := seen[tag]; duplicate {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	return result, nil
}

// NormalizeAliases validates configured aliases, removes duplicates, and sorts the result.
// Aliases use lowercase and trimmed space. Invalid aliases return no partial result.
func NormalizeAliases(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	aliases := make([]string, 0, len(values))
	for _, value := range values {
		alias := strings.ToLower(strings.TrimSpace(value))
		if alias == "" || len(alias) > MaxAliasBytes || strings.ContainsAny(alias, "\r\n\x00") {
			return nil, fmt.Errorf("project alias is invalid")
		}
		if _, exists := seen[alias]; exists {
			continue
		}
		seen[alias] = struct{}{}
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	return aliases, nil
}

// ValidateItem checks the frozen item contract without changing the item.
// Frozen aliases keep their legacy length-only check. Configured aliases use NormalizeAliases.
func ValidateItem(item Item) error {
	if item.Kind != Task && item.Kind != Note {
		return fmt.Errorf("item kind is invalid")
	}
	title := strings.TrimSpace(item.Title)
	if title == "" || len(title) > MaxTitleBytes {
		return fmt.Errorf("item title is invalid")
	}
	if len(item.Content) > MaxContentBytes {
		return fmt.Errorf("item content is too large")
	}
	if item.Kind == Note {
		if item.Due != nil || item.AllDay || item.Priority != 0 || len(item.Tags) != 0 || item.ProjectAlias != "" {
			return fmt.Errorf("note has task fields")
		}
		return nil
	}
	if item.Due == nil && item.AllDay {
		return fmt.Errorf("all-day task requires a due date")
	}
	if !ValidPriority(item.Priority) {
		return fmt.Errorf("task priority is invalid")
	}
	if _, err := NormalizeTags(item.Tags); err != nil {
		return fmt.Errorf("task tags are invalid")
	}
	if len(strings.TrimSpace(item.ProjectAlias)) > MaxAliasBytes {
		return fmt.Errorf("project alias is too large")
	}
	return nil
}
