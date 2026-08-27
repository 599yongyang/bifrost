package schemas

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsNilInterfaceRecognizesTypedNil(t *testing.T) {
	var pointer *int
	var wrapped any = pointer
	if !IsNilInterface(wrapped) {
		t.Fatal("typed nil pointer must be recognized as nil")
	}
	if IsNilInterface(Ptr(1)) {
		t.Fatal("non-nil pointer must not be recognized as nil")
	}
}

func TestStreamPanicStateBypassesRestrictedWritesButCannotBeSpoofed(t *testing.T) {
	ctx := NewBifrostContext(context.Background(), NoDeadline)
	ctx.BlockRestrictedWrites()
	ctx.SetValue(BifrostContextKeyStreamPanicked, true)
	if value := ctx.Value(BifrostContextKeyStreamPanicked); value != nil {
		t.Fatalf("public reserved write was accepted: %v", value)
	}
	ctx.SetStreamPanicState(true)
	if panicked, _ := ctx.Value(BifrostContextKeyStreamPanicked).(bool); !panicked {
		t.Fatal("internal stream panic state was not recorded")
	}
}

func TestSanitizeImageURLDefaultRejectsNonHTTPSchemes(t *testing.T) {
	// The no-args overload must keep the historical http/https-only policy. Providers
	// that legitimately accept other schemes (gs://, file://, ...) must opt in via
	// SanitizeImageURLWithAllowedSchemes — otherwise a future caller silently inherits
	// a wider attack/regression surface.
	_, err := SanitizeImageURL("gs://my-bucket/path/image.png")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `URL scheme "gs" is not allowed`)

	_, err = SanitizeImageURL("file:///etc/passwd")
	require.Error(t, err)
}

func TestSanitizeImageURLWithAllowedSchemesAcceptsOptIn(t *testing.T) {
	sanitizedURL, err := SanitizeImageURLWithAllowedSchemes(" gs://my-bucket/path/image.png ", "http", "https", "gs")
	require.NoError(t, err)
	assert.Equal(t, "gs://my-bucket/path/image.png", sanitizedURL)
}

func TestSanitizeImageURLWithAllowedSchemesRejectsUnlisted(t *testing.T) {
	_, err := SanitizeImageURLWithAllowedSchemes("gs://my-bucket/path/image.png", "http", "https")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `URL scheme "gs" is not allowed`)
}

func TestSanitizeImageURLWithEmptyAllowlistRejects(t *testing.T) {
	// Empty allowlist means "no non-data URL is acceptable" — an explicit denial,
	// not "fall back to defaults".
	_, err := SanitizeImageURLWithAllowedSchemes("https://example.com/foo.png")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no schemes permitted`)
}

func TestSanitizeImageURLDataURLUnaffectedByAllowlist(t *testing.T) {
	dataURL := "data:image/png;base64,iVBORw0KGgo="
	got, err := SanitizeImageURL(dataURL)
	require.NoError(t, err)
	assert.Equal(t, dataURL, got)

	got, err = SanitizeImageURLWithAllowedSchemes(dataURL)
	require.NoError(t, err)
	assert.Equal(t, dataURL, got)
}
