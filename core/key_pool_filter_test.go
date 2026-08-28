package bifrost

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetKeyPoolFilterSuppressesFixedKeyAndCanBeCleared(t *testing.T) {
	var upstreamHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"success","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, upstream.URL)
	client := newStreamTestClient(t, account)
	client.SetKeyPoolFilter(func(_ *schemas.BifrostContext, _ schemas.ModelProvider, _ string, _ []schemas.Key) ([]schemas.Key, error) {
		return nil, nil
	})

	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	response, bifrostErr := client.ChatCompletionRequest(ctx, keyPoolFilterChatRequest())
	require.Nil(t, response)
	require.NotNil(t, bifrostErr)
	require.NotNil(t, bifrostErr.Type)
	assert.Equal(t, "no_eligible_keys", *bifrostErr.Type)
	assert.Zero(t, upstreamHits.Load())

	client.SetKeyPoolFilter(nil)
	ctx = schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	response, bifrostErr = client.ChatCompletionRequest(ctx, keyPoolFilterChatRequest())
	require.Nil(t, bifrostErr)
	require.NotNil(t, response)
	assert.Equal(t, int32(1), upstreamHits.Load())
}

func keyPoolFilterChatRequest() *schemas.BifrostChatRequest {
	return &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI,
		Model:    "gpt-4o-mini",
		Input: []schemas.ChatMessage{{
			Role:    schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")},
		}},
	}
}

func TestSetKeyPoolFilterIsRaceSafeDuringReload(t *testing.T) {
	client := &Bifrost{}
	filter := func(_ *schemas.BifrostContext, _ schemas.ModelProvider, _ string, keys []schemas.Key) ([]schemas.Key, error) {
		return keys, nil
	}

	var waitGroup sync.WaitGroup
	for i := 0; i < 8; i++ {
		waitGroup.Add(1)
		go func(clear bool) {
			defer waitGroup.Done()
			for j := 0; j < 100; j++ {
				if clear {
					client.SetKeyPoolFilter(nil)
				} else {
					client.SetKeyPoolFilter(filter)
				}
				if loaded := client.keyPoolFilter.Load(); loaded != nil {
					_, _ = loaded.filter(nil, schemas.OpenAI, "model", nil)
				}
			}
		}(i%2 == 0)
	}
	waitGroup.Wait()
}
