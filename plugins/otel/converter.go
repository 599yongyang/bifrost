package otel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

var localMediaReferencePattern = regexp.MustCompile(`bifrost-media://[A-Za-z0-9._:-]+`)

// sessionIDHash returns the SHA-256 digest of sessionID. Callers extract
// non-overlapping slices for the trace ID (bytes 0-15) and synthetic parent
// span ID (bytes 16-23) so both are derived from a single hash invocation.
func sessionIDHash(sessionID string) [32]byte {
	return sha256.Sum256([]byte(sessionID))
}

// sessionTraceID derives a deterministic 128-bit (32 lowercase hex char) trace ID
// from an x-bf-session-id value. Used to pin every request sharing a session into one
// OTEL trace when group_traces_by_session is enabled.
func sessionTraceID(sessionID string) string {
	sum := sessionIDHash(sessionID)
	return hex.EncodeToString(sum[:16])
}

// sessionParentSpanID derives a deterministic 64-bit (16 lowercase hex char) span ID
// from an x-bf-session-id value. It serves as the synthetic (never-emitted) parent of
// each request's root span so requests render as top-level siblings under one trace.
func sessionParentSpanID(sessionID string) string {
	sum := sessionIDHash(sessionID)
	return hex.EncodeToString(sum[16:24])
}

// kvStr creates a key-value pair with a string value
func kvStr(k, v string) *KeyValue {
	return &KeyValue{Key: k, Value: &AnyValue{Value: &StringValue{StringValue: v}}}
}

// kvInt creates a key-value pair with an integer value
func kvInt(k string, v int64) *KeyValue {
	return &KeyValue{Key: k, Value: &AnyValue{Value: &IntValue{IntValue: v}}}
}

// kvDbl creates a key-value pair with a double value
func kvDbl(k string, v float64) *KeyValue {
	return &KeyValue{Key: k, Value: &AnyValue{Value: &DoubleValue{DoubleValue: v}}}
}

// kvBool creates a key-value pair with a boolean value
func kvBool(k string, v bool) *KeyValue {
	return &KeyValue{Key: k, Value: &AnyValue{Value: &BoolValue{BoolValue: v}}}
}

// kvAny creates a key-value pair with an any value
func kvAny(k string, v *AnyValue) *KeyValue {
	return &KeyValue{Key: k, Value: v}
}

// arrValue converts a list of any values to an OpenTelemetry array value
func arrValue(vals ...*AnyValue) *AnyValue {
	return &AnyValue{Value: &ArrayValue{ArrayValue: &ArrayValueValue{Values: vals}}}
}

// listValue converts a list of key-value pairs to an OpenTelemetry list value
func listValue(kvs ...*KeyValue) *AnyValue {
	return &AnyValue{Value: &ListValue{KvlistValue: &KeyValueList{Values: kvs}}}
}

// hexToBytes converts a hex string to bytes, padding/truncating as needed
func hexToBytes(hexStr string, length int) []byte {
	// Remove any non-hex characters
	cleaned := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			return r
		}
		return -1
	}, hexStr)
	// Ensure even length
	if len(cleaned)%2 != 0 {
		cleaned = "0" + cleaned
	}
	// Truncate or pad to desired length
	if len(cleaned) > length*2 {
		cleaned = cleaned[:length*2]
	} else if len(cleaned) < length*2 {
		cleaned = strings.Repeat("0", length*2-len(cleaned)) + cleaned
	}
	bytes, _ := hex.DecodeString(cleaned)
	return bytes
}

// convertTraceToResourceSpan converts a Bifrost trace to OTEL ResourceSpan for the given
// profile service name. Span filtering and instance attributes are shared across profiles;
// only the resource service name differs per profile.
func (p *OtelPlugin) convertTraceToResourceSpan(serviceName string, trace *schemas.Trace, requestHeaders []string, disableContentLogging bool, groupTracesBySession bool, disableRootSpanContent bool) *ResourceSpan {
	return p.convertTraceToResourceSpanWithMedia(serviceName, trace, requestHeaders, disableContentLogging, groupTracesBySession, disableRootSpanContent, nil)
}

func (p *OtelPlugin) convertTraceToResourceSpanWithMedia(serviceName string, trace *schemas.Trace, requestHeaders []string, disableContentLogging bool, groupTracesBySession bool, disableRootSpanContent bool, mediaRefs map[string]string) *ResourceSpan {
	reparent := p.pluginSpanFilter.BuildReparentMap(trace.Spans)
	filteredHeaders := schemas.FilterHeaders(trace.RequestHeaders, requestHeaders)

	// The x-bf-session-id header is a trace-level attribute, so it is not emitted as a span
	// attribute by default. Surface it on the root span as the OTEL-conventional session.id
	// whenever present, so traces can always be filtered/correlated by session — independent
	// of grouping.
	sessionID := getStringAttr(trace.Attributes, schemas.TraceAttrSessionID)

	// Session grouping: when enabled and this request carries an x-bf-session-id but no
	// inbound W3C traceparent (root span has no parent), pin every span to a trace ID
	// derived from the session ID so all requests in the session share one OTEL trace. The
	// root span is parented to a synthetic, never-emitted session span, making each request
	// a top-level sibling under one trace. An inbound traceparent sets RootSpan.ParentID, so
	// it takes precedence and the request stays on its distributed trace.
	traceID := trace.TraceID
	groupBySession := groupTracesBySession && sessionID != "" && trace.RootSpan != nil && trace.RootSpan.ParentID == ""
	if groupBySession {
		traceID = sessionTraceID(sessionID)
	}

	otelSpans := make([]*Span, 0, len(trace.Spans))
	for _, span := range trace.Spans {
		if !p.pluginSpanFilter.ShouldExportSpan(span) {
			continue
		}
		// disableRootSpanContent drops content from the root span only (the framework duplicates
		// input/output onto it for trace-level display); child spans keep their full content.
		spanDisableContent := disableContentLogging || (disableRootSpanContent && span == trace.RootSpan)
		otelSpan := convertSpanToOTELSpanWithMedia(traceID, span, spanDisableContent, mediaRefs)
		// If the span's direct parent was filtered, rewrite its parent ID to the
		// nearest exported ancestor so the hierarchy stays connected.
		if effectiveParent, ok := reparent[span.ParentID]; ok {
			if effectiveParent == "" {
				otelSpan.ParentSpanId = nil
			} else {
				otelSpan.ParentSpanId = hexToBytes(effectiveParent, 8)
			}
		}
		if span == trace.RootSpan {
			if sessionID != "" {
				otelSpan.Attributes = append(otelSpan.Attributes, kvStr("session.id", sessionID))
			}
			if groupBySession {
				// Parent the root to the synthetic session span so requests are siblings.
				otelSpan.ParentSpanId = hexToBytes(sessionParentSpanID(sessionID), 8)
			}
			if requestID := trace.GetRequestID(); requestID != "" {
				otelSpan.Attributes = append(otelSpan.Attributes,
					kvStr(schemas.AttrRequestID, requestID), // legacy: gen_ai.* placement of bifrost-internal attr; replaced by bifrost.request.id
					kvStr(schemas.AttrBifrostRequestID, requestID),
				)
			}
			if len(p.instanceAttrs) > 0 {
				otelSpan.Attributes = append(otelSpan.Attributes, p.instanceAttrs...)
			}
			for k, v := range filteredHeaders {
				otelSpan.Attributes = append(otelSpan.Attributes, kvStr("http.request.header."+k, v))
			}
		}
		otelSpans = append(otelSpans, otelSpan)
	}
	return &ResourceSpan{
		Resource: &resourcepb.Resource{
			Attributes: p.getResourceAttributes(serviceName),
		},
		ScopeSpans: []*ScopeSpan{{
			Scope: p.getInstrumentationScope(serviceName),
			Spans: otelSpans,
		}},
	}
}

// convertSpanToOTELSpan converts a single Bifrost span to OTEL format
func convertSpanToOTELSpan(traceID string, span *schemas.Span, disableContentLogging bool) *Span {
	return convertSpanToOTELSpanWithMedia(traceID, span, disableContentLogging, nil)
}

func convertSpanToOTELSpanWithMedia(traceID string, span *schemas.Span, disableContentLogging bool, mediaRefs map[string]string) *Span {
	otelSpan := &Span{
		TraceId:           hexToBytes(traceID, 16),
		SpanId:            hexToBytes(span.SpanID, 8),
		Name:              span.Name,
		Kind:              convertSpanKind(span.Kind),
		StartTimeUnixNano: uint64(span.StartTime.UnixNano()),
		EndTimeUnixNano:   uint64(span.EndTime.UnixNano()),
		Attributes:        convertAttributesToKeyValues(span.Attributes, disableContentLogging),
		Status:            convertSpanStatus(span.Status, span.StatusMsg),
		Events:            convertSpanEvents(span.Events, disableContentLogging),
	}
	appendImageObservationAttributes(otelSpan, span, disableContentLogging, mediaRefs)

	// Set parent span ID if present
	if span.ParentID != "" {
		otelSpan.ParentSpanId = hexToBytes(span.ParentID, 8)
	}

	return otelSpan
}

// appendImageObservationAttributes maps Bifrost's compact image summaries to the
// explicit Langfuse Observation attributes. Langfuse requires structured values to be
// JSON strings on OTEL spans.
func appendImageObservationAttributes(otelSpan *Span, span *schemas.Span, disableContentLogging bool, mediaRefs map[string]string) {
	if otelSpan == nil || span == nil || span.Attributes == nil {
		return
	}
	requestType, _ := span.Attributes[schemas.AttrLegacyRequestType].(string)
	switch requestType {
	case string(schemas.ImageGenerationRequest), string(schemas.ImageGenerationStreamRequest),
		string(schemas.ImageEditRequest), string(schemas.ImageEditStreamRequest),
		string(schemas.ImageVariationRequest):
	default:
		return
	}
	otelSpan.Attributes = append(otelSpan.Attributes,
		kvStr("langfuse.observation.type", "generation"),
		kvStr("langfuse.observation.metadata.request_type", requestType),
	)
	attrs := span.Attributes
	providerModel := firstNonEmpty(
		getStringAttr(attrs, schemas.AttrBifrostProviderModel),
		getStringAttr(attrs, schemas.AttrResponseModel),
		getStringAttr(attrs, schemas.AttrRequestModel),
	)
	publicModel := firstNonEmpty(getStringAttr(attrs, schemas.AttrBifrostPublicModel), getStringAttr(attrs, schemas.AttrRequestModel))
	provider := firstNonEmpty(getStringAttr(attrs, schemas.AttrBifrostProviderName), getStringAttr(attrs, schemas.AttrProviderName))
	routingRule := firstNonEmpty(getStringAttr(attrs, schemas.AttrBifrostRoutingRuleName), getStringAttr(attrs, schemas.AttrBifrostRoutingRuleID))
	if providerModel != "" {
		otelSpan.Attributes = append(otelSpan.Attributes,
			kvStr("langfuse.observation.model.name", providerModel),
			kvStr("langfuse.observation.metadata.provider_model", providerModel),
		)
	}
	if publicModel != "" {
		otelSpan.Attributes = append(otelSpan.Attributes, kvStr("langfuse.observation.metadata.public_model", publicModel))
	}
	if provider != "" {
		otelSpan.Attributes = append(otelSpan.Attributes, kvStr("langfuse.observation.metadata.provider", provider))
	}
	if routingRule != "" {
		otelSpan.Attributes = append(otelSpan.Attributes, kvStr("langfuse.observation.metadata.routing_rule", routingRule))
	}
	if fallbackIndex := getIntAttr(attrs, schemas.AttrBifrostFallbackIndex); fallbackIndex > 0 {
		otelSpan.Attributes = append(otelSpan.Attributes, kvStr("langfuse.observation.metadata.fallback_index", strconv.Itoa(fallbackIndex)))
	} else {
		otelSpan.Attributes = append(otelSpan.Attributes, kvStr("langfuse.observation.metadata.fallback_index", "0"))
	}
	if !span.StartTime.IsZero() && !span.EndTime.IsZero() {
		otelSpan.Attributes = append(otelSpan.Attributes, kvStr("langfuse.observation.metadata.latency_ms", strconv.FormatInt(span.EndTime.Sub(span.StartTime).Milliseconds(), 10)))
	}
	usage := map[string]any{}
	if value := getIntAttr(attrs, schemas.AttrInputTokens); value != 0 {
		usage["input"] = value
	}
	if value := getIntAttr(attrs, schemas.AttrOutputTokens); value != 0 {
		usage["output"] = value
	}
	if value := getIntAttr(attrs, schemas.AttrTotalTokens); value != 0 {
		usage["total"] = value
	}
	for key, target := range map[string]string{
		schemas.AttrInputTokenDetailsImage:  "input_image",
		schemas.AttrInputTokenDetailsText:   "input_text",
		schemas.AttrOutputTokenDetailsImage: "output_image",
		schemas.AttrOutputTokenDetailsText:  "output_text",
	} {
		if value := getIntAttr(attrs, key); value != 0 {
			usage[target] = value
		}
	}
	if len(usage) > 0 {
		otelSpan.Attributes = append(otelSpan.Attributes, kvStr("langfuse.observation.usage_details", mustJSON(usage)))
	}
	if cost := numericAttr(attrs, schemas.AttrUsageCost); cost != 0 {
		otelSpan.Attributes = append(otelSpan.Attributes, kvStr("langfuse.observation.cost_details", mustJSON(map[string]any{"total": cost})))
	}
	for key, target := range map[string]string{
		schemas.AttrErrorTypeSpec:          "langfuse.observation.metadata.error_type",
		schemas.AttrErrorCode:              "langfuse.observation.metadata.error_code",
		schemas.AttrHTTPResponseStatusCode: "langfuse.observation.metadata.http_status_code",
	} {
		if value := fmt.Sprint(attrs[key]); value != "<nil>" && value != "" {
			otelSpan.Attributes = append(otelSpan.Attributes, kvStr(target, value))
		}
	}
	if span.Status == schemas.SpanStatusError {
		otelSpan.Attributes = append(otelSpan.Attributes, kvStr("langfuse.observation.level", "ERROR"))
		if message := getStringAttr(attrs, schemas.AttrError); message != "" {
			otelSpan.Attributes = append(otelSpan.Attributes,
				kvStr("langfuse.observation.status_message", message),
				kvStr("langfuse.observation.metadata.error_message", message),
			)
		}
	}
	if disableContentLogging {
		return
	}
	if input, ok := span.Attributes[schemas.AttrBifrostImageInput].(string); ok && input != "" {
		input = replaceMediaReferences(input, mediaRefs)
		otelSpan.Attributes = append(otelSpan.Attributes, kvStr("langfuse.observation.input", input))
		if parameters := imageModelParameters(input); parameters != "" {
			otelSpan.Attributes = append(otelSpan.Attributes, kvStr("langfuse.observation.model.parameters", parameters))
		}
	}
	if output, ok := span.Attributes[schemas.AttrBifrostImageOutput].(string); ok && output != "" {
		output = replaceMediaReferences(output, mediaRefs)
		otelSpan.Attributes = append(otelSpan.Attributes, kvStr("langfuse.observation.output", output))
	}
}

func imageModelParameters(input string) string {
	var value map[string]any
	if err := schemas.Unmarshal([]byte(input), &value); err != nil {
		return ""
	}
	delete(value, "prompt")
	delete(value, "images")
	delete(value, "mask")
	delete(value, "image_count")
	if len(value) == 0 {
		return ""
	}
	return mustJSON(value)
}

func numericAttr(attrs map[string]any, key string) float64 {
	switch value := attrs[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}

func mustJSON(value any) string {
	data, err := schemas.MarshalSorted(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func replaceMediaReferences(value string, mediaRefs map[string]string) string {
	for localRef, token := range mediaRefs {
		value = strings.ReplaceAll(value, localRef, token)
	}
	return localMediaReferencePattern.ReplaceAllString(value, "")
}

// getResourceAttributes returns the resource attributes for the OTEL span
func (p *OtelPlugin) getResourceAttributes(serviceName string) []*KeyValue {
	attrs := []*KeyValue{
		kvStr("service.name", serviceName),
		kvStr("service.version", p.bifrostVersion),
		kvStr("telemetry.sdk.name", "bifrost"),
		kvStr("telemetry.sdk.language", "go"),
	}
	// Add environment attributes
	attrs = append(attrs, p.attributesFromEnvironment...)
	return attrs
}

// getInstrumentationScope returns the instrumentation scope for OTEL
func (p *OtelPlugin) getInstrumentationScope(serviceName string) *commonpb.InstrumentationScope {
	return &commonpb.InstrumentationScope{
		Name:    serviceName,
		Version: p.bifrostVersion,
	}
}

// convertAttributesToKeyValues converts map[string]any to OTEL KeyValue slice.
// When disableContentLogging is true, attributes carrying message/input/output content or
// tool definitions/arguments/results are dropped so only metadata is exported.
func convertAttributesToKeyValues(attrs map[string]any, disableContentLogging bool) []*KeyValue {
	if attrs == nil {
		return nil
	}
	kvs := make([]*KeyValue, 0, len(attrs))
	for k, v := range attrs {
		if disableContentLogging && schemas.IsContentAttribute(k) {
			continue
		}
		// Overhead is not exported to connectors; it is still computed and stored in
		// the logs DB. Skip it here so it does not ride the exported span.
		if k == schemas.AttrBifrostOverheadDurationMs {
			continue
		}
		kv := anyToKeyValue(k, v)
		if kv != nil {
			kvs = append(kvs, kv)
		}
	}
	return kvs
}

// anyToKeyValue converts any Go value to OTEL KeyValue
func anyToKeyValue(key string, value any) *KeyValue {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case string:
		if v == "" {
			return nil
		}
		return kvStr(key, v)
	case int:
		return kvInt(key, int64(v))
	case int32:
		return kvInt(key, int64(v))
	case int64:
		return kvInt(key, v)
	case uint:
		return kvInt(key, int64(v))
	case uint32:
		return kvInt(key, int64(v))
	case uint64:
		return kvInt(key, int64(v))
	case float32:
		return kvDbl(key, float64(v))
	case float64:
		return kvDbl(key, v)
	case bool:
		return kvBool(key, v)
	case []string:
		if len(v) == 0 {
			return nil
		}
		vals := make([]*AnyValue, len(v))
		for i, s := range v {
			vals[i] = &AnyValue{Value: &StringValue{StringValue: s}}
		}
		return kvAny(key, arrValue(vals...))
	case []int:
		if len(v) == 0 {
			return nil
		}
		vals := make([]*AnyValue, len(v))
		for i, n := range v {
			vals[i] = &AnyValue{Value: &IntValue{IntValue: int64(n)}}
		}
		return kvAny(key, arrValue(vals...))
	case []int64:
		if len(v) == 0 {
			return nil
		}
		vals := make([]*AnyValue, len(v))
		for i, n := range v {
			vals[i] = &AnyValue{Value: &IntValue{IntValue: n}}
		}
		return kvAny(key, arrValue(vals...))
	case []float64:
		if len(v) == 0 {
			return nil
		}
		vals := make([]*AnyValue, len(v))
		for i, n := range v {
			vals[i] = &AnyValue{Value: &DoubleValue{DoubleValue: n}}
		}
		return kvAny(key, arrValue(vals...))
	case []any:
		if len(v) == 0 {
			return nil
		}
		vals := make([]*AnyValue, 0, len(v))
		for _, item := range v {
			if kv := anyToKeyValue("_", item); kv != nil {
				vals = append(vals, kv.Value)
			}
		}
		if len(vals) == 0 {
			return nil
		}
		return kvAny(key, arrValue(vals...))
	case map[string]any:
		if len(v) == 0 {
			return nil
		}
		kvList := make([]*KeyValue, 0, len(v))
		for k, val := range v {
			kv := anyToKeyValue(k, val)
			if kv != nil {
				kvList = append(kvList, kv)
			}
		}
		return kvAny(key, listValue(kvList...))
	default:
		data, err := schemas.MarshalSorted(v)
		if err != nil {
			return kvStr(key, fmt.Sprintf("%v", v))
		}
		var generic any
		if err := schemas.Unmarshal(data, &generic); err != nil {
			return kvStr(key, string(data))
		}
		return anyToKeyValue(key, generic)
	}
}

// convertSpanKind maps Bifrost SpanKind to OTEL SpanKind
func convertSpanKind(kind schemas.SpanKind) tracepb.Span_SpanKind {
	switch kind {
	case schemas.SpanKindLLMCall:
		return tracepb.Span_SPAN_KIND_CLIENT
	case schemas.SpanKindHTTPRequest:
		return tracepb.Span_SPAN_KIND_SERVER
	case schemas.SpanKindPlugin:
		return tracepb.Span_SPAN_KIND_INTERNAL
	case schemas.SpanKindInternal:
		return tracepb.Span_SPAN_KIND_INTERNAL
	case schemas.SpanKindRetry:
		return tracepb.Span_SPAN_KIND_INTERNAL
	case schemas.SpanKindFallback:
		return tracepb.Span_SPAN_KIND_INTERNAL
	case schemas.SpanKindMCPTool:
		return tracepb.Span_SPAN_KIND_CLIENT
	case schemas.SpanKindMCPClient:
		return tracepb.Span_SPAN_KIND_CLIENT
	case schemas.SpanKindEmbedding:
		return tracepb.Span_SPAN_KIND_CLIENT
	case schemas.SpanKindSpeech:
		return tracepb.Span_SPAN_KIND_CLIENT
	case schemas.SpanKindTranscription:
		return tracepb.Span_SPAN_KIND_CLIENT
	default:
		return tracepb.Span_SPAN_KIND_UNSPECIFIED
	}
}

// convertSpanStatus maps Bifrost SpanStatus to OTEL Status
func convertSpanStatus(status schemas.SpanStatus, msg string) *tracepb.Status {
	switch status {
	case schemas.SpanStatusOk:
		return &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK}
	case schemas.SpanStatusError:
		return &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: msg}
	default:
		return &tracepb.Status{Code: tracepb.Status_STATUS_CODE_UNSET}
	}
}

// convertSpanEvents converts Bifrost span events to OTEL events
func convertSpanEvents(events []schemas.SpanEvent, disableContentLogging bool) []*Event {
	if len(events) == 0 {
		return nil
	}
	otelEvents := make([]*Event, len(events))
	for i, event := range events {
		otelEvents[i] = &Event{
			TimeUnixNano: uint64(event.Timestamp.UnixNano()),
			Name:         event.Name,
			Attributes:   convertAttributesToKeyValues(event.Attributes, disableContentLogging),
		}
	}
	return otelEvents
}
