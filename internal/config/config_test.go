package config

import "testing"

func TestRefusesToRunWithoutAKey(t *testing.T) {
	t.Setenv("SIDECAR_API_KEY", "")
	if err := LoadFromEnv().Validate(); err == nil {
		t.Fatal("a sidecar without an API key would serve anyone's files")
	}
	t.Setenv("SIDECAR_INSECURE", "true")
	if err := LoadFromEnv().Validate(); err != nil {
		t.Fatalf("explicitly insecure: %v", err)
	}
	t.Setenv("SIDECAR_INSECURE", "")
	t.Setenv("SIDECAR_API_KEY", "k")
	if err := LoadFromEnv().Validate(); err != nil {
		t.Fatal(err)
	}
}
