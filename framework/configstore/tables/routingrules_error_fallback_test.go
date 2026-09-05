package tables

import "testing"

func TestHasContentSafetyRecognitionCluesRejectsWhitespaceOnlyMatchers(t *testing.T) {
	rule := TableRoutingErrorFallback{
		Scenario: "content_policy",
		Supplement: &TableRoutingErrorFallbackSupplement{
			ErrorCodes:         []string{"  "},
			ErrorTypes:         []string{"\t"},
			StatusCodes:        []int{0, 600},
			MessageContainsAny: []string{"\n"},
		},
	}
	if rule.HasContentSafetyRecognitionClues() {
		t.Fatal("whitespace-only supplemental matchers must not enable recognition-only persistence")
	}

	rule = TableRoutingErrorFallback{
		When: TableRoutingErrorFallbackCondition{
			Categories:      []string{"content_policy"},
			ErrorCodes:      []string{" "},
			ErrorTypes:      []string{"\t"},
			StatusCodes:     []int{0, 600},
			MessageContains: []string{"\n"},
		},
	}
	if rule.HasContentSafetyRecognitionClues() {
		t.Fatal("whitespace-only legacy matchers must not enable recognition-only persistence")
	}
}

func TestHasContentSafetyRecognitionCluesAcceptsNormalizedMatchers(t *testing.T) {
	rule := TableRoutingErrorFallback{
		Scenario: " content_policy ",
		Supplement: &TableRoutingErrorFallbackSupplement{
			MessageContainsAny: []string{" vendor moderation gate "},
		},
	}
	if !rule.HasContentSafetyRecognitionClues() {
		t.Fatal("non-empty supplemental matcher should enable recognition-only persistence")
	}
}
