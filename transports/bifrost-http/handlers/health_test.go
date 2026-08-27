package handlers

import (
	"sync"
	"testing"
)

func TestRunHealthProbeContainsPanic(t *testing.T) {
	SetLogger(&mockLogger{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var probeErrors []string
	wg.Add(1)
	go runHealthProbe(&wg, &mu, &probeErrors, "log store", func() error {
		panic("database driver panic")
	})
	wg.Wait()
	if len(probeErrors) != 1 || probeErrors[0] != "log store not available" {
		t.Fatalf("probe errors = %v", probeErrors)
	}
}
