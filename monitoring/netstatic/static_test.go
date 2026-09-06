package netstatic

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentStartAndRepeatedRestart(t *testing.T) {
	originalPath := SaveFilePath
	SaveFilePath = filepath.Join(t.TempDir(), "net_static.json")
	t.Cleanup(func() {
		if err := Stop(); err != nil {
			t.Errorf("stop accounting: %v", err)
		}
		SaveFilePath = originalPath
	})

	for cycle := 0; cycle < 20; cycle++ {
		var starters sync.WaitGroup
		for worker := 0; worker < 8; worker++ {
			starters.Go(func() {
				if err := StartOrContinue(); err != nil {
					t.Errorf("start accounting: %v", err)
				}
			})
		}
		starters.Wait()
		// Reconfiguration replaces worker channels just like a stop/start cycle.
		if err := SetNewConfig(NetStaticConfig{}); err != nil {
			t.Fatalf("restart accounting workers: %v", err)
		}
		if err := Stop(); err != nil {
			t.Fatalf("stop accounting: %v", err)
		}
	}
}

func TestConcurrentGettersInitializeSafely(t *testing.T) {
	originalPath := SaveFilePath
	SaveFilePath = filepath.Join(t.TempDir(), "net_static.json")
	mu.Lock()
	running = false
	store = NetStatic{}
	staticCache = nil
	config = NetStaticConfig{}
	mu.Unlock()
	t.Cleanup(func() {
		if err := Stop(); err != nil {
			t.Errorf("stop accounting: %v", err)
		}
		SaveFilePath = originalPath
	})

	var getters sync.WaitGroup
	for i := 0; i < 32; i++ {
		getters.Go(func() {
			if _, err := GetNetStatic(); err != nil {
				t.Errorf("get net static: %v", err)
			}
			if _, err := GetNetStaticBetween(0, 0); err != nil {
				t.Errorf("get net static between: %v", err)
			}
			if _, err := GetTotalTraffic(); err != nil {
				t.Errorf("get total traffic: %v", err)
			}
			if _, err := GetTotalTrafficBetween(0, 0); err != nil {
				t.Errorf("get total traffic between: %v", err)
			}
		})
	}
	getters.Wait()
}

func TestSetNewConfigRejectsInvalidDurations(t *testing.T) {
	originalPath := SaveFilePath
	SaveFilePath = filepath.Join(t.TempDir(), "net_static.json")
	if err := Stop(); err != nil {
		t.Fatalf("stop accounting: %v", err)
	}
	t.Cleanup(func() {
		_ = Stop()
		SaveFilePath = originalPath
	})

	invalid := []NetStaticConfig{
		{DataPreserveDay: -1},
		{DataPreserveDay: 1e300},
		{DetectInterval: -1},
		{DetectInterval: 1e-300},
		{SaveInterval: 0.0, DetectInterval: 1e300},
	}
	for _, cfg := range invalid {
		if err := SetNewConfig(cfg); err == nil {
			t.Fatalf("SetNewConfig(%+v) unexpectedly succeeded", cfg)
		}
	}

	if err := os.WriteFile(SaveFilePath, []byte(`{"config":{"data_preserve_day":-1,"detect_interval":-1,"save_interval":1e300}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := StartOrContinue(); err != nil {
		t.Fatalf("invalid persisted config prevented startup: %v", err)
	}
	if config.DataPreserveDay <= 0 || config.DetectInterval <= 0 || config.SaveInterval <= 0 {
		t.Fatalf("invalid persisted config was not sanitized: %+v", config)
	}
}
