package schemas

import (
	"encoding/json"
	"strings"
)

const (
	maxFailureRecognitionDepth  = 6
	maxFailureRecognitionNodes  = 128
	maxFailureRecognitionLength = 4096
	maxFailureRecognitionJSON   = 64 * 1024
)

var failureRecognitionKeyReplacer = strings.NewReplacer("_", "", "-", "", ".", "", " ", "")

// ExtractFailureRecognitionSignals recovers only bounded error metadata from a
// provider response. Structured responses ignore unrelated fields; a bounded
// top-level plain-text error body is treated as the provider error message.
func ExtractFailureRecognitionSignals(raw any) FailureRecognitionSignals {
	collector := &failureRecognitionSignalCollector{}
	collector.collect(raw, "", 0)
	return collector.signals
}

// MergeFailureRecognitionSignals combines bounded internal signal summaries.
func MergeFailureRecognitionSignals(groups ...FailureRecognitionSignals) FailureRecognitionSignals {
	var merged FailureRecognitionSignals
	for _, group := range groups {
		merged.ErrorCodes = appendUniqueRecognitionStrings(merged.ErrorCodes, group.ErrorCodes...)
		merged.ErrorTypes = appendUniqueRecognitionStrings(merged.ErrorTypes, group.ErrorTypes...)
		merged.Messages = appendUniqueRecognitionStrings(merged.Messages, group.Messages...)
	}
	return merged
}

type failureRecognitionSignalCollector struct {
	signals FailureRecognitionSignals
	nodes   int
}

func (collector *failureRecognitionSignalCollector) collect(value any, parentKey string, depth int) {
	if collector == nil || value == nil || depth > maxFailureRecognitionDepth || collector.nodes >= maxFailureRecognitionNodes {
		return
	}
	collector.nodes++
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalizedKey := normalizeRecognitionKey(key)
			if normalizedKey == "safetyviolations" {
				collector.signals.ErrorCodes = appendUniqueRecognitionStrings(collector.signals.ErrorCodes, "safety_violations")
			}
			collector.collect(child, normalizedKey, depth+1)
		}
	case map[string]string:
		for key, child := range typed {
			normalizedKey := normalizeRecognitionKey(key)
			if normalizedKey == "safetyviolations" {
				collector.signals.ErrorCodes = appendUniqueRecognitionStrings(collector.signals.ErrorCodes, "safety_violations")
			}
			collector.collect(child, normalizedKey, depth+1)
		}
	case []any:
		for _, child := range typed {
			collector.collect(child, parentKey, depth+1)
		}
	case []string:
		for _, child := range typed {
			collector.collect(child, parentKey, depth+1)
		}
	case string:
		if parentKey == "" {
			if collector.collectJSON([]byte(typed), depth) {
				return
			}
			parentKey = "message"
		}
		collector.add(parentKey, typed)
	case []byte:
		if parentKey == "" {
			if collector.collectJSON(typed, depth) {
				return
			}
			parentKey = "message"
		}
		collector.add(parentKey, string(typed))
	}
}

func (collector *failureRecognitionSignalCollector) collectJSON(raw []byte, depth int) bool {
	if len(raw) == 0 || len(raw) > maxFailureRecognitionJSON || !json.Valid(raw) {
		return false
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false
	}
	collector.collect(decoded, "", depth+1)
	return true
}

func (collector *failureRecognitionSignalCollector) add(key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if len(value) > maxFailureRecognitionLength {
		value = value[:maxFailureRecognitionLength]
	}
	switch key {
	case "code", "errorcode", "finishreason", "stopreason", "safetyviolations":
		collector.signals.ErrorCodes = appendUniqueRecognitionStrings(collector.signals.ErrorCodes, value)
	case "type", "errortype":
		collector.signals.ErrorTypes = appendUniqueRecognitionStrings(collector.signals.ErrorTypes, value)
	case "error", "errors", "message", "errormessage", "detail", "reason", "errordescription":
		collector.signals.Messages = appendUniqueRecognitionStrings(collector.signals.Messages, value)
	}
}

func normalizeRecognitionKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	return failureRecognitionKeyReplacer.Replace(key)
}

func appendUniqueRecognitionStrings(dst []string, values ...string) []string {
	for _, value := range values {
		if value == "" {
			continue
		}
		found := false
		for _, existing := range dst {
			if existing == value {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, value)
		}
	}
	return dst
}
